export interface Summary {
  total: number
  email_consent: number
  sms_consent: number
  cities: string[]
  buyers: number
  repeat_buyers: number
  active_30d: number
  avg_order_value: number
}

export interface Audience {
  id: string
  name: string
  description: string
  size: number
  email_eligible: number
  sms_eligible: number
}

export interface AudienceReport {
  category: string
  categories: string[]
  audiences: Audience[]
}

export interface Customer {
  id: number
  first_name: string
  last_name: string
  email: string
  phone: string | null
  city: string
  language: string
  email_consent: boolean
  sms_consent: boolean
  created_at: string
  last_active_at: string
  orders: number
  total_spent: number
  last_order_at: string | null
}

export interface CustomerPage {
  items: Customer[]
  total: number
  page: number
  page_size: number
}

/** '' means "any". */
export type ConsentFilter = '' | 'true' | 'false'

export interface CustomerQuery {
  page: number
  pageSize: number
  city: string
  emailConsent: ConsentFilter
  smsConsent: ConsentFilter
  /** Audience id; '' means everyone. */
  segment: string
  category: string
  /** Server-side sort; '' means default order. */
  sort?: SortField | ''
  order?: SortOrder
}

export type SortOrder = 'asc' | 'desc'

/** Sort keys accepted by GET /api/customers (whitelisted on the backend). */
export type SortField = 'name' | 'city' | 'language' | 'orders' | 'total_spent' | 'last_order_at' | 'last_active_at' | 'created_at'

export interface Order {
  id: number
  category: string
  amount: number
  placed_at: string
}

export interface CartAddition {
  id: number
  category: string
  added_at: string
}

export interface CustomerDetail extends Customer {
  last_cart_at: string | null
  carts: number
  /** Newest first, capped by the backend (20). */
  recent_orders: Order[]
  recent_carts: CartAddition[]
}

// The API may still be starting (cold `make dev`): the proxy answers 502/503/504
// or the connection fails. Retry idempotent GETs with backoff before surfacing an error.
const RETRY_STATUSES = new Set([502, 503, 504])
const MAX_RETRIES = 8

function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(signal.reason)
    const t = setTimeout(resolve, ms)
    signal?.addEventListener('abort', () => { clearTimeout(t); reject(signal.reason) }, { once: true })
  })
}

async function fetchWithRetry(path: string, signal?: AbortSignal): Promise<Response> {
  for (let attempt = 0; ; attempt++) {
    try {
      const res = await fetch(path, { signal })
      if (!RETRY_STATUSES.has(res.status) || attempt >= MAX_RETRIES) return res
    } catch (err) {
      if (signal?.aborted || attempt >= MAX_RETRIES) throw err
    }
    await sleep(Math.min(500 * 1.5 ** attempt, 3000), signal)
  }
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetchWithRetry(path, signal)
  if (!res.ok) {
    let message = `Request failed (${res.status})`
    try {
      const body: { error?: string } = await res.json()
      if (body.error) message = body.error
    } catch {
      // keep the generic message
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export function fetchSummary(signal?: AbortSignal) {
  return get<Summary>('/api/customers/summary', signal)
}

export function fetchCustomers(q: CustomerQuery, signal?: AbortSignal) {
  const params = new URLSearchParams({ page: String(q.page), page_size: String(q.pageSize) })
  if (q.city) params.set('city', q.city)
  if (q.emailConsent) params.set('email_consent', q.emailConsent)
  if (q.smsConsent) params.set('sms_consent', q.smsConsent)
  if (q.segment) {
    params.set('segment', q.segment)
    if (q.category) params.set('category', q.category)
  }
  if (q.sort) {
    params.set('sort', q.sort)
    params.set('order', q.order ?? 'asc')
  }
  return get<CustomerPage>(`/api/customers?${params}`, signal)
}

export function fetchCustomer(id: number, signal?: AbortSignal) {
  return get<CustomerDetail>(`/api/customers/${id}`, signal)
}

export function fetchAudiences(category: string, signal?: AbortSignal) {
  const params = new URLSearchParams()
  if (category) params.set('category', category)
  return get<AudienceReport>(`/api/audiences?${params}`, signal)
}

// --- AI audience suggestions ------------------------------------------------

export interface AudienceCounts {
  total: number
  email_eligible: number
  sms_eligible: number
}

export interface AudienceAlternative {
  id: string
  name: string
  reasoning: string
  primary: boolean
  match: 'all' | 'any'
  conditions: unknown[]
  conditions_text: string[]
  counts: AudienceCounts
  /** Valid rules, but no customer matches today. */
  empty: boolean
}

export type SuggestionOutcome = 'supported' | 'needs_clarification' | 'unsupported' | 'invalid' 

export interface SuggestionResponse {
  source: 'claude' | 'mock'
  model?: string
  outcome: SuggestionOutcome
  message: string
  questions: string[]
  unsupported_requirements: string[]
  suggestions: string[]
  total_customers: number
  alternatives: AudienceAlternative[]
  warnings: string[]
}

/** Error from a POST that may carry validation warnings from the backend. */
export class SuggestError extends Error {
  warnings: string[]
  constructor(message: string, warnings: string[] = []) {
    super(message)
    this.warnings = warnings
  }
}

export async function suggestAudiences(brief: string, signal?: AbortSignal): Promise<SuggestionResponse> {
  const res = await fetch('/api/segmentation/suggest', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ brief }),
    signal,
  })
  if (!res.ok) {
    let message = `Request failed (${res.status})`
    let warnings: string[] = []
    try {
      const body: { error?: string; warnings?: string[] } = await res.json()
      if (body.error) message = body.error
      warnings = body.warnings ?? []
    } catch {
      // keep the generic message
    }
    throw new SuggestError(message, warnings)
  }
  return res.json() as Promise<SuggestionResponse>
}

export interface PreviewCustomer {
  first_name: string
  last_name: string
  email: string
  city: string
  language: string
  email_consent: boolean
  sms_consent: boolean
  orders: number
  total_spent: number
}

export async function previewCustomers(a: Pick<AudienceAlternative, 'match' | 'conditions'>, signal?: AbortSignal): Promise<PreviewCustomer[]> {
  const res = await fetch('/api/segmentation/preview', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ match: a.match, conditions: a.conditions }),
    signal,
  })
  if (!res.ok) {
    let message = `Request failed (${res.status})`
    try {
      const body: { error?: string } = await res.json()
      if (body.error) message = body.error
    } catch {
      // keep the generic message
    }
    throw new Error(message)
  }
  return ((await res.json()) as { customers: PreviewCustomer[] }).customers
}

// --- Campaigns: content drafts and approval -----------------------------------

export interface ContentSettings {
  tone: string
  occasion: string
  length: string
  cta: string
}

export interface SettingOptions {
  tone: string[]
  occasion: string[]
  length: string[]
  cta: string[]
}

export interface Content {
  sms_enabled: boolean
  email_enabled: boolean
  settings: ContentSettings
  sms_body: string
  email_subject: string
  email_body: string
}

export interface Issue {
  field: string
  code: string
  message: string
  /** Blocking issues prevent approval; others are warnings. */
  blocking: boolean
  /** Content language the issue is about, if any. */
  language?: string
  /** Content unit at review time, e.g. "Variant B" or a group name. */
  scope?: string
}

/** One language version of a message set. */
export interface Version {
  sms_body: string
  email_subject: string
  email_body: string
  base_hash?: string
}

export interface LangStatus {
  language: string
  status: 'base' | 'ok' | 'stale' | 'missing'
}

export type DraftOrigin = 'initial' | 'regenerate' | 'candidate' | 'manual' | 'duplicate'
export type Personalization = 'standard' | 'groups' | 'individual'


export interface Draft extends Content {
  version: number
  created_at: string
  updated_at: string
  source: 'claude' | 'mock' | 'manual'
  feedback: string
  origin: DraftOrigin
  parent_version: number | null
  /** Language of the draft's own text; translations hold the others. */
  language: string
  translations: Record<string, Version>
  localization: LangStatus[]
  issues: Issue[]
  notice?: string
}


export interface CampaignAudience {
  name: string
  match: 'all' | 'any'
  conditions: unknown[]
  conditions_text: string[]
  counts: AudienceCounts
  /** Longest first name among reachable members; bounds personalized length. */
  max_first_name_len: number
  /** Reachable members per content language. */
  languages: Record<string, number>
}

export interface Campaign {
  id: number
  created_at: string
  brief: string
  audience: CampaignAudience
  status: 'draft' | 'approved'
  personalization: Personalization
  selected_version: number | null
  variants: Variant[]
  /** Content languages present in the audience, base language first. */
  languages: string[]
  base_language: string
  groups_note?: { outcome: 'groups' | 'not_meaningful'; message: string; warnings: string[] } | null
  individual_applied_at: string | null
  approved_version: number | null
  approved_at: string | null
}

export interface Variant {
  label: 'A' | 'B' | 'C'
  position: number
  draft_version: number
}

export interface Condition {
  field: string
  op: string
  strings: string[]
  numbers: number[]
  category: string
  within_days: number
}

export interface Group {
  /** 0 is the remainder group ("everyone else"). */
  position: number
  name: string
  description: string
  match: 'all' | 'any'
  conditions: Condition[]
  conditions_text: string[]
  size: number
  languages: Record<string, number>
  content: Record<string, Version>
  /** Message strategy planned when the groups were proposed (empty for "everyone else"). */
  angle: string
  /** How the generated message builds on the group's behavior. */
  rationale: string
  /** false: no honest reason for a different message was found (null before generation). */
  differentiated: boolean | null
  status: 'proposed' | 'generating' | 'generated' | 'failed'
  error?: string
  stale: boolean
  issues: Issue[]
}

export interface PersonalMessage {
  customer_id: number
  first_name: string
  language: string
  status: 'pending' | 'generated' | 'fallback' | 'failed'
  reason?: string
  content: Version
  input_tokens: number
  output_tokens: number
  updated_at: string
}

export interface IndividualSummary {
  total: number
  pending: number
  generated: number
  fallback: number
  failed: number
  stale: number
  running: boolean
  applied: boolean
  /** Most customers that get an individually written message; everyone else gets the base draft. */
  limit: number
  /** Every picked customer (at most limit). */
  samples: PersonalMessage[]
}

export interface KindCost {
  calls: number
  input_tokens: number
  output_tokens: number
  usd: number | null
}

/** AI generation cost so far (not SMS/email delivery cost). */
export interface Cost extends KindCost {
  by_kind: Record<string, KindCost>
}

export interface Estimate {
  calls: number
  input_tokens: number
  output_tokens: number
  usd: number | null
  model: string
  basis: 'observed' | 'default'
}

export interface Member {
  id: number
  first_name: string
  language: string
  city: string
  email_consent: boolean
  sms_consent: boolean
  order_count: number
  total_spent: number
  last_order_at: string | null
  last_cart_at: string | null
  last_active_at: string
  created_at: string
  group: number
}

export interface Candidate extends Member {
  has_history: boolean
}

export interface PersonalInput {
  first_name: string
  city: string
  order_count: number
  days_since_last_order: number | null
  recent_orders: { category: string; days_ago: number; amount_try: number }[]
  recent_cart_additions: { category: string; days_ago: number }[]
}

export interface SampleResult {
  message: PersonalMessage
  input: PersonalInput | null
  remaining: number
  fallbacks: number
  estimate: Estimate
}

export interface ResolvedMessage {
  customer_id: number
  first_name: string
  channel: 'sms' | 'email'
  variant_position?: number
  group_position?: number
  language: string
  source: 'template' | 'group' | 'individual' | 'fallback'
  subject?: string
  body: string
}

export interface Bucket {
  position: number
  label: string
  recipients: number
  sms: number
  email: number
  share: number
}

export interface Plan {
  recipients: number
  messages: number
  by_channel: Record<string, number>
  by_language: Record<string, number>
  by_source: Record<string, number>
  variants: Bucket[]
  groups: Bucket[]
  prior_buyers: number
  never_ordered: number
  samples: ResolvedMessage[]
}

export interface Review {
  ready: boolean
  mode: Personalization
  version: number
  issues: Issue[]
  plan: Plan
  cost: Cost
}


export interface CampaignView {
  campaign: Campaign
  drafts: Draft[]
  groups: Group[]
  individual: IndividualSummary
  cost: Cost
  /** Background jobs running for this campaign. */
  jobs: { groups: boolean; individual: boolean }
  options: SettingOptions
}

/** Error from the campaign API; issues are set when approval validation failed. */
export class CampaignError extends Error {
  status: number
  issues: Issue[]
  constructor(message: string, status: number, issues: Issue[] = []) {
    super(message)
    this.status = status
    this.issues = issues
  }
}

async function send<T>(method: string, path: string, body: unknown, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal,
  })
  if (!res.ok) {
    let message = `Request failed (${res.status})`
    let issues: Issue[] = []
    try {
      const b: { error?: string; issues?: Issue[] } = await res.json()
      if (b.error) message = b.error
      issues = b.issues ?? []
    } catch {
      // keep the generic message
    }
    throw new CampaignError(message, res.status, issues)
  }
  return res.json() as Promise<T>
}

export function createCampaign(brief: string, a: AudienceAlternative) {
  return send<CampaignView>('POST', '/api/campaigns', {
    brief,
    audience: { name: a.name, match: a.match, conditions: a.conditions },
  })
}

export function getCampaign(id: number, signal?: AbortSignal) {
  return send<CampaignView>('GET', `/api/campaigns/${id}`, undefined, signal)
}

export type DraftEdit = Content & { translations: Record<string, Version> }

export async function saveDraft(id: number, version: number, c: DraftEdit) {
  return (await send<{ draft: Draft }>('PUT', `/api/campaigns/${id}/drafts/${version}`, c)).draft
}

/** A blank draft, or a copy of fromVersion. */
export async function createDraft(id: number, fromVersion = 0) {
  return (await send<{ draft: Draft }>('POST', `/api/campaigns/${id}/drafts`, { from_version: fromVersion })).draft
}

/** (Re)translates missing or stale languages of a draft. */
export async function localizeDraft(id: number, version: number) {
  return (await send<{ draft: Draft }>('POST', `/api/campaigns/${id}/drafts/${version}/localize`, {})).draft
}

/** The draft a single-message campaign sends (and the base for personalization). */
export function selectDraft(id: number, version: number) {
  return send<CampaignView>('PUT', `/api/campaigns/${id}/selection`, { version })
}

/** Variants in order A, B, C; [] disables A/B testing. */
export function setVariants(id: number, versions: number[]) {
  return send<CampaignView>('PUT', `/api/campaigns/${id}/variants`, { versions })
}

export function setPersonalization(id: number, mode: Personalization, baseVersion = 0) {
  return send<CampaignView>('PUT', `/api/campaigns/${id}/personalization`, { mode, base_version: baseVersion })
}

export function proposeGroups(id: number) {
  return send<CampaignView>('POST', `/api/campaigns/${id}/groups/propose`, {})
}

export function groupsEstimate(id: number) {
  return send<Estimate>('GET', `/api/campaigns/${id}/groups/estimate`, undefined)
}

export function generateGroups(id: number) {
  return send<{ status: string }>('POST', `/api/campaigns/${id}/groups/generate`, {})
}

export async function groupExamples(id: number, position: number) {
  return (await send<{ examples: { label: string; member: Member }[] }>('GET', `/api/campaigns/${id}/groups/${position}/examples`, undefined)).examples
}

export function saveGroup(id: number, position: number, content: Record<string, Version>) {
  return send<CampaignView>('PUT', `/api/campaigns/${id}/groups/${position}`, { content })
}

export async function individualCandidates(id: number) {
  return (await send<{ candidates: Candidate[] }>('GET', `/api/campaigns/${id}/individual/candidates`, undefined)).candidates
}

export function individualEstimate(id: number) {
  return send<SampleResult>('GET', `/api/campaigns/${id}/individual/estimate`, undefined)
}

export function individualSample(id: number, customerId: number) {
  return send<SampleResult>('POST', `/api/campaigns/${id}/individual/sample`, { customer_id: customerId })
}

export function applyIndividual(id: number) {
  return send<{ status: string }>('POST', `/api/campaigns/${id}/individual/apply`, {})
}

export function stopIndividual(id: number) {
  return send<{ status: string }>('POST', `/api/campaigns/${id}/individual/stop`, {})
}

/** Consistency gate, recipient plan and cost; version is the Standard draft to approve. */
export function reviewCampaign(id: number, version: number, signal?: AbortSignal) {
  return send<Review>('GET', `/api/campaigns/${id}/review?version=${version}`, undefined, signal)
}

export function approveCampaign(id: number, version: number) {
  return send<CampaignView>('POST', `/api/campaigns/${id}/approve`, { version })
}

// --- delivery ----------------------------------------------------------------------

export type DeliveryStatus = 'not_sent' | 'queued' | 'sending' | 'completed' | 'completed_with_failures'

/** Delivery records by status; retries are attempts beyond the first. */
export interface DeliveryCounts {
  total: number
  queued: number
  sending: number
  retrying: number
  sent: number
  failed: number
  skipped: number
  retries: number
}

export interface DeliveryBucket extends DeliveryCounts {
  position: number
}

/** A campaign's delivery state, read from the database (not simulated). */
export interface DeliveryReport {
  campaign_id: number
  status: DeliveryStatus
  dispatched_at: string | null
  finished_at: string | null
  totals: DeliveryCounts
  channels: Record<string, DeliveryCounts>
  variants: DeliveryBucket[]
  groups: DeliveryBucket[]
  errors: { status: 'retrying' | 'failed' | 'skipped'; reason: string; count: number }[]
  next_retry_at: string | null
  worker_seen_at: string | null
  /** Failed deliveries that can be retried (not permanent rejections). */
  retryable_failed: number
}

export type MessageStatus = 'queued' | 'sending' | 'retrying' | 'sent' | 'failed' | 'skipped'
/** List filter: a status, or pending (= queued or sending). */
export type MessageFilter = '' | 'pending' | 'retrying' | 'sent' | 'failed' | 'skipped'

/** One delivery: a recipient on one channel. */
export interface DeliveryMessage {
  id: number
  customer_id: number
  name: string
  /** Masked current address. */
  to: string
  channel: 'sms' | 'email'
  variant_position: number | null
  group_position: number | null
  language: string
  status: MessageStatus
  attempts: number
  last_code: string | null
  last_error: string | null
  /** Rejected by the provider; never retryable. */
  permanent: boolean
  retryable: boolean
  next_attempt_at: string | null
  sent_at: string | null
  provider_message_id: string | null
  updated_at: string
}

export interface MessagePage {
  total: number
  page: number
  page_size: number
  items: DeliveryMessage[]
}

/** One provider call. http_status null means no response arrived. */
export interface DeliveryAttempt {
  attempt: number
  started_at: string
  latency_ms: number
  http_status: number | null
  code: string | null
  outcome: 'sent' | 'retrying' | 'failed'
  error: string | null
  message_id: string | null
  manual: boolean
}

export interface MessageDetail extends DeliveryMessage {
  subject: string
  body: string
  attempts_history: DeliveryAttempt[]
}

export interface CampaignSummary {
  id: number
  created_at: string
  brief: string
  audience: string
  status: 'draft' | 'approved'
  dispatched_at: string | null
  delivery_status: DeliveryStatus
  messages: number
  sent: number
  failed: number
}

/** Queues the approved campaign's messages once; repeating it changes nothing. */
export function sendCampaign(id: number) {
  return send<DeliveryReport>('POST', `/api/campaigns/${id}/send`, undefined)
}

export function getDelivery(id: number, signal?: AbortSignal) {
  return send<DeliveryReport>('GET', `/api/campaigns/${id}/delivery`, undefined, signal)
}

export function listMessages(id: number, f: { status: MessageFilter; channel: '' | 'sms' | 'email'; page: number }, signal?: AbortSignal) {
  const q = new URLSearchParams({ status: f.status, channel: f.channel, page: String(f.page), page_size: '20' })
  return send<MessagePage>('GET', `/api/campaigns/${id}/messages?${q}`, undefined, signal)
}

export function getMessage(id: number, messageId: number) {
  return send<MessageDetail>('GET', `/api/campaigns/${id}/messages/${messageId}`, undefined)
}

/** Requeues one failed delivery (same record and idempotency key). 409 explains why it can't be. */
export function retryMessage(id: number, messageId: number) {
  return send<{ requeued: number; permanent: number }>('POST', `/api/campaigns/${id}/messages/${messageId}/retry`, undefined)
}

/** Requeues every failed delivery of the campaign that wasn't permanently rejected. */
export function retryFailed(id: number) {
  return send<{ requeued: number; permanent: number }>('POST', `/api/campaigns/${id}/retry-failed`, undefined)
}

export async function listCampaigns() {
  return (await send<{ campaigns: CampaignSummary[] }>('GET', '/api/campaigns', undefined)).campaigns
}


/** Field updates streamed while a draft is written (current value, not a delta). */
export type StreamField = 'tone' | 'occasion' | 'length' | 'cta' | 'sms_body' | 'email_subject' | 'email_body'

/**
 * Generates a draft over server-sent events (POST, so not EventSource).
 * onField receives each field's latest value; resolves with the stored draft.
 * fromVersion 0 writes the first draft; otherwise that draft is revised.
 */
export async function streamGeneration(
  id: number,
  body: { from_version: number; feedback?: string; intent?: 'regenerate' | 'candidate' },
  onField: (field: StreamField, value: string) => void,
  signal?: AbortSignal,
  onLocalizing?: (languages: string[]) => void,
): Promise<Draft> {
  const res = await fetch(`/api/campaigns/${id}/generate`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  })
  if (!res.ok || !res.body || !res.headers.get('Content-Type')?.startsWith('text/event-stream')) {
    let message = `Request failed (${res.status})`
    try {
      const b: { error?: string } = await res.json()
      if (b.error) message = b.error
    } catch {
      // keep the generic message
    }
    throw new CampaignError(message, res.status)
  }
  const reader = res.body.pipeThrough(new TextDecoderStream()).getReader()
  let buf = ''
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buf += value
    let sep: number
    while ((sep = buf.indexOf('\n\n')) >= 0) {
      const block = buf.slice(0, sep)
      buf = buf.slice(sep + 2)
      let event = 'message'
      let data = ''
      for (const line of block.split('\n')) {
        if (line.startsWith('event: ')) event = line.slice(7)
        else if (line.startsWith('data: ')) data += line.slice(6)
      }
      if (!data) continue
      const payload = JSON.parse(data)
      if (event === 'field') onField(payload.field, payload.value)
      else if (event === 'localizing') onLocalizing?.(payload.languages ?? [])
      else if (event === 'done') return payload.draft as Draft
      else if (event === 'error') throw new CampaignError(payload.message, 0)
    }
  }
  throw new CampaignError('The connection closed before the draft was finished. Please retry.', 0)
}

/** Supported personalization variable and channel limits (mirrors the backend). */
export const FIRST_NAME = '{{first_name}}'
export const MAX_SMS = 160
export const MAX_SUBJECT = 80

export function render(template: string, firstName: string) {
  return template.split(FIRST_NAME).join(firstName)
}

/** Character count after substituting a first name of nameLen characters. */
export function personalizedLength(template: string, nameLen: number) {
  const uses = template.split(FIRST_NAME).length - 1
  return [...template].length + uses * (nameLen - FIRST_NAME.length)
}

export const LANGUAGE_NAMES: Record<string, string> = { tr: 'Turkish', en: 'English' }
export const languageName = (l: string) => LANGUAGE_NAMES[l] ?? l.toUpperCase()

/** Content language a customer receives (mirrors the backend fallback rule). */
export const contentLanguage = (l: string) => (l in LANGUAGE_NAMES ? l : 'en')

/** Small USD amounts need more precision than cents. */
export function formatUSD(v: number | null | undefined) {
  if (v == null) return '—'
  if (v === 0) return '$0'
  if (v < 0.01) return `$${v.toFixed(4)}`
  return `$${v.toFixed(2)}`
}

export function formatTokens(n: number) {
  return n >= 10000 ? `${Math.round(n / 1000)}k` : new Intl.NumberFormat('en-US').format(n)
}
