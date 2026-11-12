<script setup lang="ts">
import { ref, watch } from 'vue'
import { getCampaign, type CampaignView } from '../api'
import DeliveryPanel from '../components/DeliveryPanel.vue'
import { deliveryId, href } from '../router'

/** Sending and monitoring page of one campaign (#/campaigns/{id}/delivery); survives a refresh. */
const view = ref<CampaignView | null>(null)
const error = ref('')
const number = new Intl.NumberFormat('en-US')

watch(
  deliveryId,
  async (id) => {
    if (!id) return
    view.value = null
    error.value = ''
    try {
      view.value = await getCampaign(id)
    } catch (e) {
      error.value = e instanceof Error ? e.message : 'Could not load the campaign'
    }
  },
  { immediate: true },
)
</script>

<template>
  <section class="campaign" aria-labelledby="delivery-page-title">
    <nav class="crumbs" aria-label="Breadcrumb">
      <a :href="href('/campaigns')">Campaigns</a>
      <span aria-hidden="true">/</span>
      <span aria-current="page">Campaign #{{ deliveryId }}</span>
    </nav>
    <p v-if="error" class="error-text" role="alert">{{ error }}</p>
    <template v-if="view">
      <header>
        <h1 id="delivery-page-title">{{ view.campaign.status === 'approved' ? 'Delivery' : 'Not approved yet' }}</h1>
        <p>
          Audience: <strong>{{ view.campaign.audience.name }}</strong> ·
          {{ number.format(view.campaign.audience.counts.total) }} people
        </p>
        <p class="brief">“{{ view.campaign.brief }}”</p>
      </header>
      <DeliveryPanel v-if="view.campaign.status === 'approved'" :key="view.campaign.id" :view="view" />
      <p v-else class="muted">This campaign is still a draft. Approve it before sending.</p>
    </template>
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
header {
  margin-bottom: 20px;
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
.brief {
  max-width: 760px;
  margin-top: 6px;
  font-size: 13px;
  font-style: italic;
}
</style>
