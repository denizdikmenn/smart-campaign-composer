import { computed, inject, onUnmounted, provide, reactive, ref, watch, type InjectionKey } from 'vue'
import {
  getCampaign,
  saveDraft,
  streamGeneration,
  type CampaignView,
  type Draft,
  type DraftEdit,
  type StreamField,
} from './api'

export type StudioPhase = 'generating' | 'editing' | 'reviewing' | 'approved'

/** Steps inside the campaign workspace (after the audience is chosen). */
export type Step = 'content' | 'strategy' | 'review'

const clone = <T>(v: T): T => JSON.parse(JSON.stringify(v))

/**
 * Shared state of one campaign in the composer: the server view, the draft
 * being edited (with autosave), streaming generation and background-job
 * polling. Created by ContentStudio and injected by its panels.
 */
export function createCampaignStore(initial: CampaignView, onPhase: (p: StudioPhase, draft: number) => void) {
  const id = initial.campaign.id
  const view = ref<CampaignView>(clone(initial))
  const campaign = computed(() => view.value.campaign)
  const drafts = computed(() => view.value.drafts)
  const approved = computed(() => campaign.value.status === 'approved')
  const currentVersion = ref(initial.campaign.selected_version ?? initial.drafts.at(-1)?.version ?? 0)
  const step = ref<Step>(initial.campaign.status === 'approved' ? 'review' : 'content')
  const current = computed(() => drafts.value.find((d) => d.version === currentVersion.value) ?? null)
  const variantOf = (version: number) => campaign.value.variants.find((v) => v.draft_version === version) ?? null

  // --- autosave of the draft being edited ------------------------------------------
  const saveState = ref<'saved' | 'dirty' | 'saving' | 'error'>('saved')
  const saveError = ref('')
  let saveTimer: ReturnType<typeof setTimeout> | undefined
  let saving: Promise<void> | null = null

  const editOf = (d: Draft): DraftEdit => ({
    sms_enabled: d.sms_enabled,
    email_enabled: d.email_enabled,
    settings: { ...d.settings },
    sms_body: d.sms_body,
    email_subject: d.email_subject,
    email_body: d.email_body,
    translations: clone(d.translations),
  })

  // Only a change within the same version is an edit (switching drafts or a
  // refreshed view also changes the watched value).
  watch(
    () => [currentVersion.value, current.value && JSON.stringify(editOf(current.value))] as const,
    ([version, now], [prevVersion, before]) => {
      if (version !== prevVersion || approved.value || !now || !before || now === before || applying) return
      saveState.value = 'dirty'
      clearTimeout(saveTimer)
      saveTimer = setTimeout(() => void runSave(), 700)
    },
  )

  async function runSave() {
    const d = current.value
    if (!d) return
    clearTimeout(saveTimer)
    saveState.value = 'saving'
    const body = editOf(d)
    saving = (async () => {
      try {
        const saved = await saveDraft(id, d.version, body)
        d.issues = saved.issues
        d.localization = saved.localization
        d.updated_at = saved.updated_at
        // Base hashes may change server-side (edited translations count as reviewed).
        for (const [l, v] of Object.entries(saved.translations)) {
          if (d.translations[l]) d.translations[l].base_hash = v.base_hash
        }
        saveState.value = JSON.stringify(editOf(d)) === JSON.stringify(body) ? 'saved' : 'dirty'
        saveError.value = ''
      } catch (e) {
        saveState.value = 'error'
        saveError.value = e instanceof Error ? e.message : 'Could not save'
      }
    })()
    await saving
    saving = null
  }

  async function flushSave() {
    if (saving) await saving
    if (saveState.value === 'dirty' || saveState.value === 'error') await runSave()
    return saveState.value !== 'error'
  }

  // --- server view ---------------------------------------------------------------------
  let applying = false // replacing the view must not look like an edit

  function apply(v: CampaignView) {
    applying = true
    // Keep the local object of the draft being edited if it has unsaved changes.
    if (saveState.value !== 'saved' && current.value) {
      const i = v.drafts.findIndex((d) => d.version === current.value!.version)
      if (i >= 0) v.drafts[i] = current.value
    }
    view.value = v
    queueMicrotask(() => (applying = false))
  }

  async function refresh() {
    apply(await getCampaign(id))
  }

  function upsertDraft(d: Draft) {
    applying = true
    const i = view.value.drafts.findIndex((x) => x.version === d.version)
    if (i >= 0) view.value.drafts[i] = d
    else view.value.drafts.push(d)
    queueMicrotask(() => (applying = false))
  }

  async function selectDraft(v: number) {
    await flushSave()
    currentVersion.value = v
  }

  // --- generic actions -----------------------------------------------------------------
  const busy = ref('') // label of the running action
  const error = ref('')

  /** Runs fn after saving edits, then refreshes the view; errors land in `error`. */
  async function act<T>(label: string, fn: () => Promise<T>, opts: { refresh?: boolean } = {}): Promise<T | undefined> {
    if (busy.value) return
    if (!(await flushSave())) return
    busy.value = label
    error.value = ''
    try {
      const out = await fn()
      if (opts.refresh !== false) await refresh()
      return out
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Something went wrong'
      return undefined
    } finally {
      busy.value = ''
    }
  }

  // --- streaming generation --------------------------------------------------------------
  const live = reactive({
    active: false,
    fields: {} as Partial<Record<StreamField, string>>,
    localizing: [] as string[],
    error: '',
    notice: '',
    from: 0,
    intent: 'regenerate' as 'regenerate' | 'candidate',
    feedback: '',
  })
  const nextVersion = computed(() => (drafts.value.at(-1)?.version ?? 0) + 1)
  let streamCtl: AbortController | null = null

  /** Streams a new draft; existing drafts are never touched until it is stored. */
  async function generate(from: number, intent: 'regenerate' | 'candidate' = 'regenerate', feedback = ''): Promise<Draft | null> {
    if (live.active || approved.value) return null
    if (!(await flushSave())) return null
    streamCtl?.abort()
    streamCtl = new AbortController()
    Object.assign(live, { active: true, fields: {}, localizing: [], error: '', notice: '', from, intent, feedback })
    onPhase('generating', nextVersion.value)
    try {
      const d = await streamGeneration(
        id,
        { from_version: from, feedback, intent },
        (field, value) => (live.fields[field] = value),
        streamCtl.signal,
        (langs) => (live.localizing = langs),
      )
      upsertDraft(d)
      currentVersion.value = d.version
      live.notice = d.notice ?? ''
      void refresh() // cost and statuses
      return d
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return null
      live.error = e instanceof Error ? e.message : 'Generation failed'
      return null
    } finally {
      live.active = false
      onPhase('editing', currentVersion.value)
    }
  }

  function stopGeneration() {
    streamCtl?.abort()
    live.active = false
    live.error = 'Generation stopped. Your drafts are unchanged.'
    onPhase('editing', currentVersion.value)
  }

  // --- background jobs (group messages, individual messages) -----------------------------
  let poll: ReturnType<typeof setInterval> | undefined
  watch(
    () => view.value.jobs.groups || view.value.jobs.individual,
    (running) => {
      clearInterval(poll)
      if (running) poll = setInterval(() => void refresh().catch(() => {}), 2000)
    },
    { immediate: true },
  )

  onUnmounted(() => {
    streamCtl?.abort()
    clearInterval(poll)
    clearTimeout(saveTimer)
    if (saveState.value === 'dirty') void runSave() // best effort
  })

  return {
    id,
    step,
    view,
    campaign,
    drafts,
    approved,
    currentVersion,
    current,
    variantOf,
    saveState,
    saveError,
    flushSave,
    refresh,
    apply,
    upsertDraft,
    selectDraft,
    busy,
    error,
    act,
    live,
    nextVersion,
    generate,
    stopGeneration,
    onPhase,
  }
}

export type CampaignStore = ReturnType<typeof createCampaignStore>

const key: InjectionKey<CampaignStore> = Symbol('campaign')

export function provideCampaign(store: CampaignStore) {
  provide(key, store)
}

export function useCampaign(): CampaignStore {
  const s = inject(key)
  if (!s) throw new Error('useCampaign() outside ContentStudio')
  return s
}

/** "Draft 4" or "Draft 4 · Variant B". */
export function draftLabel(store: CampaignStore, version: number) {
  const v = store.variantOf(version)
  return v ? `Draft ${version} · Variant ${v.label}` : `Draft ${version}`
}

export const ORIGIN_LABELS: Record<string, string> = {
  initial: 'Generated',
  regenerate: 'Regenerated',
  candidate: 'A/B candidate',
  manual: 'Written manually',
  duplicate: 'Copy',
}
