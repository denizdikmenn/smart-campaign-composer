<script setup lang="ts">
import { computed, ref } from 'vue'
import type { AudienceAlternative } from '../api'
import BriefComposer from '../components/BriefComposer.vue'
import StepIndicator from '../components/StepIndicator.vue'
import { href } from '../router'

const number = new Intl.NumberFormat('en-US')

// Planned stages: DESIGN.md §2 stage nav, covering REQUIREMENTS.md's workflow
// (brief -> audience, content generation, human review/approval, delivery + monitoring).
const STEPS = [
  { label: 'Audience', hint: 'Brief and targeting', available: true },
  { label: 'Content', hint: 'SMS and email drafts', available: false },
  { label: 'Review', hint: 'Preview and approve', available: false },
  { label: 'Delivery', hint: 'Send and monitor', available: false },
]
const hasOptions = ref(false) // a response with selectable audiences is on screen

// Sub-progress inside the Audience stage, shown on the stepper.
const phase = computed(() => {
  if (confirmed.value) return { progress: 1, status: 'Audience set' }
  if (chosen.value) return { progress: 0.75, status: 'Confirm your choice' }
  if (hasOptions.value) return { progress: 0.5, status: 'Compare options' }
  return { progress: 0.15, status: 'Describe your campaign' }
})

const chosen = ref<AudienceAlternative | null>(null) // alternative picked in the brief composer
const confirmed = ref(false) // user pressed "Use this audience"
// Reach is per channel; zero on both means nobody can be messaged.
const noReach = computed(() => !!chosen.value && chosen.value.counts.email_eligible + chosen.value.counts.sms_eligible === 0)

function onChoose(a: AudienceAlternative | null) {
  chosen.value = a
  confirmed.value = false // a new/changed selection invalidates the confirmation
}
</script>

<template>
  <section class="campaign" aria-labelledby="campaign-title">
    <nav class="crumbs" aria-label="Breadcrumb">
      <a :href="href('/campaigns')">Campaigns</a>
      <span aria-hidden="true">/</span>
      <span aria-current="page">New campaign</span>
    </nav>
    <StepIndicator :steps="STEPS" :current="0" :progress="phase.progress" :status="phase.status" />
    <header>
      <h1 id="campaign-title">What campaign do you want to create?</h1>
      <p>Describe it in your own words, then compare audience options.</p>
    </header>
    <BriefComposer @select="onChoose" @generated="hasOptions = $event" />

    <div v-if="chosen" class="action-bar" role="region" aria-label="Selected audience">
      <p v-if="confirmed" role="status">
        <strong>{{ chosen.name }}</strong> is set as the audience. Content, review and delivery are not built yet, so this
        is where the flow stops for now.
      </p>
      <p v-else role="status">
        <strong>{{ number.format(chosen.counts.total) }} people selected</strong> · {{ chosen.name }}
        <span class="reach">
          Email {{ number.format(chosen.counts.email_eligible) }} · SMS {{ number.format(chosen.counts.sms_eligible) }}
          (channels overlap)
        </span>
        <span v-if="noReach" class="warn">No one in this audience has consented to email or SMS. Choose another or refine the brief.</span>
      </p>
      <button v-if="!confirmed" type="button" class="cta" :disabled="noReach" @click="confirmed = true">
        Use this audience
      </button>
    </div>
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
