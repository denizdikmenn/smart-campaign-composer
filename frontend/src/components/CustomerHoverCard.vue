<script setup lang="ts">
import { computed } from 'vue'
import type { Customer, CustomerDetail } from '../api'

/**
 * Quick-look card shown while hovering a table row. Purely supplementary:
 * everything here is also in the detail drawer, which is keyboard reachable,
 * so the card is aria-hidden and ignores the pointer.
 */
const props = defineProps<{
  customer: Customer
  detail: CustomerDetail | null
  /** Bounding box of the hovered row. */
  anchor: DOMRect
}>()

const dateFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })
const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'TRY', maximumFractionDigits: 0 })
const date = (iso: string | null) => (iso ? dateFormat.format(new Date(iso)) : '—')

const CARD_HEIGHT = 230 // approximate; used only to flip above the row near the viewport bottom
const position = computed(() => {
  const left = Math.min(props.anchor.left + 64, window.innerWidth - 336)
  return props.anchor.bottom + CARD_HEIGHT + 8 > window.innerHeight
    ? { left: `${left}px`, bottom: `${window.innerHeight - props.anchor.top + 6}px` }
    : { left: `${left}px`, top: `${props.anchor.bottom + 6}px` }
})
</script>

<template>
  <Teleport to="body">
    <div class="hover-card" :style="position" aria-hidden="true">
      <p class="name">{{ customer.first_name }} {{ customer.last_name }}</p>
      <p class="muted">{{ customer.city }} · {{ customer.phone ?? 'No phone' }}</p>

      <dl class="stats">
        <div>
          <dt>Orders</dt>
          <dd>{{ customer.orders }}</dd>
        </div>
        <div>
          <dt>Lifetime</dt>
          <dd>{{ customer.orders ? money.format(customer.total_spent) : '—' }}</dd>
        </div>
        <div>
          <dt>Cart adds</dt>
          <dd><template v-if="detail">{{ detail.carts }}</template><span v-else class="skeleton-line inline" /></dd>
        </div>
      </dl>

      <p class="label">Recent orders</p>
      <ul v-if="detail && detail.recent_orders.length" class="orders">
        <li v-for="o in detail.recent_orders.slice(0, 3)" :key="o.id">
          <span class="cat">{{ o.category }}</span>
          <span class="muted">{{ date(o.placed_at) }}</span>
          <span class="amt">{{ money.format(o.amount) }}</span>
        </li>
      </ul>
      <p v-else-if="detail" class="muted">No orders yet.</p>
      <div v-else aria-hidden="true"><div class="skeleton-line" /><div class="skeleton-line" /><div class="skeleton-line short" /></div>

      <p class="hint">Click for full details</p>
    </div>
  </Teleport>
</template>

<style scoped>
.hover-card {
  position: fixed;
  z-index: 40;
  width: 320px;
  padding: 14px 16px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 16px 40px rgb(32 77 60 / 0.18);
  pointer-events: none;
  font-size: 13px;
  animation: pop 0.15s ease-out both;
}
p {
  margin: 0;
}
.name {
  font-size: 15px;
  font-weight: 600;
}
.muted {
  color: var(--text-muted);
}
.stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 8px;
  margin: 10px 0;
  padding: 10px 0;
  border-block: 1px solid var(--border);
}
dt {
  font-size: 11px;
  color: var(--text-muted);
}
dd {
  margin: 0;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.label {
  margin-bottom: 4px;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
}
.orders {
  display: grid;
  gap: 4px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.orders li {
  display: grid;
  grid-template-columns: 1fr auto auto;
  gap: 10px;
}
.cat {
  text-transform: capitalize;
}
.amt {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.hint {
  margin-top: 10px;
  font-size: 12px;
  color: var(--text-muted);
}
@keyframes pop {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
}
</style>
