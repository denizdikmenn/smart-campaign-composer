<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { selectDraft, setPersonalization, setVariants } from '../api'
import { ORIGIN_LABELS, useCampaign } from '../campaign'
import ExperimentPanel from './ExperimentPanel.vue'
import GroupsPanel from './GroupsPanel.vue'
import IndividualPanel from './IndividualPanel.vue'

/**
 * How the campaign is sent: one message, an A/B test, behavioral groups or
 * individual messages. These are alternatives, so they are one choice.
 */
type Strategy = 'single' | 'ab' | 'groups' | 'individual'

const store = useCampaign()
const { campaign, drafts, current, approved } = store

const derived = computed<Strategy>(() =>
  campaign.value.variants.length ? 'ab' : campaign.value.personalization === 'standard' ? 'single' : campaign.value.personalization,
)
// "A/B test" can be chosen before two variants exist (nothing stored yet).
const chosen = ref<Strategy>(derived.value)
watch(derived, (d) => {
  if (!(chosen.value === 'ab' && d === 'single')) chosen.value = d
})

const OPTIONS: { id: Strategy; title: string; what: string; cost: string }[] = [
  { id: 'single', title: 'Single message', what: 'One SMS and email for everyone.', cost: 'No extra AI calls' },
  { id: 'ab', title: 'A/B test', what: 'Compare 2–3 drafts on equal random shares.', cost: '1 call per new candidate' },
  { id: 'groups', title: 'Behavioral groups', what: 'A tailored message per customer group.', cost: '≈ 1 call per group' },
  { id: 'individual', title: 'Individual', what: 'A unique message per customer.', cost: '1 call per recipient' },
]

async function choose(next: Strategy) {
  if (next === chosen.value || approved.value) return
  if (campaign.value.variants.length && next !== 'ab' && !confirm('This ends the A/B test. Your drafts are kept.')) return
  await store.act('Switching…', async () => {
    if (campaign.value.variants.length && next !== 'ab') await setVariants(store.id, [])
    const mode = next === 'groups' || next === 'individual' ? next : 'standard'
    if (campaign.value.personalization !== mode) await setPersonalization(store.id, mode, base.value)
  })
  if (!store.error.value) chosen.value = next
}

// The draft that is sent (single) or personalized (groups, individual).
const base = computed(() => campaign.value.selected_version ?? current.value?.version ?? 0)
function setBase(v: number) {
  void store.act('Saving…', () => selectDraft(store.id, v))
}
onMounted(() => {
  if (!campaign.value.selected_version && current.value && !approved.value) setBase(current.value.version)
})
</script>

<template>
  <section class="panel" aria-labelledby="strategy-title">
    <header>
      <h2 id="strategy-title">How should this campaign be sent?</h2>
      <span class="muted">Every option is localized automatically.</span>
    </header>

    <div class="options" role="radiogroup" aria-label="Sending strategy">
      <button
        v-for="o in OPTIONS"
        :key="o.id"
        type="button"
        role="radio"
        class="option"
        :aria-checked="chosen === o.id"
        :disabled="approved || !!store.busy.value || store.live.active"
        @click="choose(o.id)"
      >
        <strong>{{ o.title }}</strong>
        <span>{{ o.what }}</span>
        <span class="cost">{{ o.cost }}</span>
      </button>
    </div>

    <label v-if="chosen !== 'ab'" class="base">
      {{ chosen === 'single' ? 'Send' : 'Personalize' }}
      <select :value="base" :disabled="approved || !!store.busy.value" @change="setBase(Number(($event.target as HTMLSelectElement).value))">
        <option v-for="d in drafts" :key="d.version" :value="d.version">Draft {{ d.version }} · {{ ORIGIN_LABELS[d.origin] }}</option>
      </select>
    </label>

    <ExperimentPanel v-if="chosen === 'ab'" />
    <GroupsPanel v-else-if="chosen === 'groups'" />
    <IndividualPanel v-else-if="chosen === 'individual'" />
  </section>
</template>

<style scoped>
.options {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(170px, 1fr));
  gap: 10px;
}
.option {
  display: grid;
  gap: 4px;
  align-content: start;
  padding: 12px 14px;
  font: inherit;
  text-align: left;
  color: inherit;
  background: var(--surface-muted);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  cursor: pointer;
}
.option[aria-checked='true'] {
  border-color: var(--accent);
  background: var(--selected-surface);
  box-shadow: inset 0 0 0 1px var(--accent);
}
.option:disabled {
  cursor: not-allowed;
}
.option:disabled:not([aria-checked='true']) {
  opacity: 0.55;
}
.option span {
  font-size: 12px;
  color: var(--text-muted);
}
.option .cost {
  margin-top: 4px;
  font-weight: 600;
  color: var(--text);
}
.base {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  font-weight: 600;
}
</style>
