<script setup lang="ts">
import { computed, ref } from 'vue'
import { createCampaign, type AudienceAlternative, type CampaignView } from '../api'
import BriefComposer from '../components/BriefComposer.vue'
import ContentStudio, { type Step, type StudioPhase } from '../components/ContentStudio.vue'
import StepIndicator from '../components/StepIndicator.vue'
import { href, navigate } from '../router'

const number = new Intl.NumberFormat('en-US')

// Planned stages: DESIGN.md §2 stage nav, covering REQUIREMENTS.md's workflow
// (brief -> audience, content generation, human review/approval, delivery + monitoring).
const STEPS = computed(() => [
  { label: 'Audience', hint: 'Brief and targeting', available: true },
  { label: 'Content', hint: 'SMS and email drafts', available: true },
  { label: 'Strategy', hint: 'A/B test or personalization', available: true },
  { label: 'Review', hint: 'Check and approve', available: true },
  // Sending lives on its own page so it survives refreshes.
  { label: 'Delivery', hint: 'Send and monitor', available: studio.value.phase === 'approved' },
])
const hasOptions = ref(false) // a response with selectable audiences is on screen

const chosen = ref<AudienceAlternative | null>(null) // alternative picked in the brief composer
const chosenBrief = ref('')
// Reach is per channel; zero on both means nobody can be messaged.
const noReach = computed(() => !!chosen.value && chosen.value.counts.email_eligible + chosen.value.counts.sms_eligible === 0)

// Content stage: the campaign is stored once the audience is confirmed.
const campaign = ref<CampaignView | null>(null)
const creating = ref(false)
const createError = ref('')
const studio = ref<{ phase: StudioPhase; draft: number }>({ phase: 'generating', draft: 1 })
const step = ref<Step>('content')
const studioRef = ref<InstanceType<typeof ContentStudio> | null>(null)
const STEP_INDEX: Record<Step, number> = { content: 1, strategy: 2, review: 3 }
const STEP_BY_INDEX: Step[] = ['content', 'content', 'strategy', 'review']

function onStepSelect(i: number) {
  if (i === 4 && campaign.value) {
    navigate(`/campaigns/${campaign.value.campaign.id}/delivery`)
    return
  }
  if (i === 0) {
    if (studio.value.phase !== 'approved') backToAudience()
    return
  }
  void studioRef.value?.go(STEP_BY_INDEX[i]!)
}

function onChoose(a: AudienceAlternative | null, brief: string) {
  chosen.value = a
  chosenBrief.value = brief
  createError.value = ''
}

async function confirmAudience() {
  if (!chosen.value || creating.value) return
  creating.value = true
  createError.value = ''
  try {
    studio.value = { phase: 'generating', draft: 1 }
    campaign.value = await createCampaign(chosenBrief.value, chosen.value)
    window.scrollTo({ top: 0 })
  } catch (e) {
    createError.value = e instanceof Error ? e.message : 'Could not save the campaign'
  } finally {
    creating.value = false
  }
}

// Leaves the stored draft campaign as is; confirming again starts a new one.
function backToAudience() {
  campaign.value = null
}

// Stage and sub-progress, shown on the stepper.
const phase = computed(() => {
  if (campaign.value) {
    const s = studio.value
    const current = STEP_INDEX[step.value]
    if (s.phase === 'approved') return { current: 3, progress: 1, status: 'Approved' }
    if (s.phase === 'generating') return { current, progress: 0.4, status: `Writing Draft ${s.draft}` }
    if (step.value === 'strategy') return { current, progress: 0.5, status: 'Choose how to send' }
    if (step.value === 'review') return { current, progress: 0.6, status: 'Final review' }
    return { current, progress: 0.6, status: `Editing Draft ${s.draft}` }
  }
  if (chosen.value) return { current: 0, progress: 0.75, status: 'Confirm your choice' }
  if (hasOptions.value) return { current: 0, progress: 0.5, status: 'Compare options' }
  return { current: 0, progress: 0.15, status: 'Describe your campaign' }
})
</script>

<template>
  <section class="campaign" aria-labelledby="campaign-title">
    <nav class="crumbs" aria-label="Breadcrumb">
      <a :href="href('/campaigns')">Campaigns</a>
      <span aria-hidden="true">/</span>
      <span aria-current="page">New campaign</span>
    </nav>
    <StepIndicator
      :steps="STEPS"
      :current="phase.current"
      :progress="phase.progress"
      :status="phase.status"
      :reachable="campaign && studio.phase !== 'generating' ? (studio.phase === 'approved' ? 4 : 3) : undefined"
      @select="onStepSelect"
    />
    <header v-if="!campaign">
      <h1 id="campaign-title">What campaign do you want to create?</h1>
      <p>Describe it in your own words, then compare audience options.</p>
    </header>
    <header v-else class="content-head">
      <div>
        <h1 id="campaign-title">{{ studio.phase === 'approved' ? 'Campaign approved' : { content: 'Prepare your messages', strategy: 'Choose how to send', review: 'Review and approve' }[step] }}</h1>
        <p>
          Audience: <strong>{{ campaign.campaign.audience.name }}</strong> ·
          {{ number.format(campaign.campaign.audience.counts.total) }} people
          (email {{ number.format(campaign.campaign.audience.counts.email_eligible) }} · SMS
          {{ number.format(campaign.campaign.audience.counts.sms_eligible) }})
        </p>
        <p class="brief">“{{ campaign.campaign.brief }}”</p>
      </div>
      <button v-if="studio.phase !== 'approved'" type="button" class="ghost" @click="backToAudience">Change audience</button>
    </header>

    <!-- Kept mounted so going back restores the brief, options and selection. -->
    <div v-show="!campaign">
      <BriefComposer @select="onChoose" @generated="hasOptions = $event" />

      <div v-if="chosen" class="action-bar" role="region" aria-label="Selected audience">
        <p role="status">
          <strong>{{ number.format(chosen.counts.total) }} people selected</strong> · {{ chosen.name }}
          <span class="reach">
            Email {{ number.format(chosen.counts.email_eligible) }} · SMS {{ number.format(chosen.counts.sms_eligible) }}
            (channels overlap)
          </span>
          <span v-if="noReach" class="warn">No one in this audience has consented to email or SMS. Choose another or refine the brief.</span>
          <span v-if="createError" class="warn" role="alert">{{ createError }}</span>
        </p>
        <button type="button" class="cta" :disabled="noReach || creating" :aria-busy="creating" @click="confirmAudience">
          {{ creating ? 'Saving campaign' : 'Use this audience' }}
        </button>
      </div>
    </div>

    <ContentStudio
      v-if="campaign"
      :key="campaign.campaign.id"
      :initial="campaign"
      ref="studioRef"
      @phase="(p, d) => (studio = { phase: p, draft: d })"
      @step="step = $event"
    />
  </section>
</template>

<style scoped>
.crumbs {
  display: flex;
  gap: 8px;
  margin-bottom: 8px;
  font-size: 12px;
  color: var(--text-muted);
}
.crumbs a {
  color: inherit;
  font-weight: 500;
}
.crumbs a:hover {
  color: var(--text);
}
header {
  margin-bottom: 24px;
}
h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.3;
  letter-spacing: -0.02em;
}
header p {
  margin: 2px 0 0;
  color: var(--text-muted);
}
.content-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
}
.content-head .brief {
  max-width: 760px;
  margin-top: 6px;
  font-size: 13px;
  font-style: italic;
}
.ghost {
  padding: 8px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  color: inherit;
  font: inherit;
  white-space: nowrap;
  cursor: pointer;
}
.action-bar {
  position: sticky;
  bottom: 16px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 18px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: var(--shadow);
}
.action-bar p {
  margin: 0;
}
.action-bar .reach,
.action-bar .warn {
  display: block;
  font-size: 12px;
  color: var(--text-muted);
}
.action-bar .warn {
  color: var(--danger);
}
.cta {
  padding: 10px 20px;
  border: 0;
  border-radius: var(--radius-control);
  background: var(--accent-soft);
  color: var(--accent);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}
.cta:hover:not(:disabled) {
  background: #d1df9f;
}
.cta:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
@media (max-width: 820px) {
  .action-bar {
    flex-direction: column;
    align-items: stretch;
  }
}
</style>
