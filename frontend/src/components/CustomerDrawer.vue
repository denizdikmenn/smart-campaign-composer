<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import type { Customer, CustomerDetail } from '../api'
import LangChip from './LangChip.vue'
import Avatar from './Avatar.vue'

const props = defineProps<{
  /** Row the drawer was opened from; null = closed. */
  customer: Customer | null
  detail: CustomerDetail | null
  loading: boolean
  error: string
}>()
const emit = defineEmits<{ close: []; retry: [] }>()

const dateFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })
const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'TRY', maximumFractionDigits: 0 })
const date = (iso: string | null | undefined) => (iso ? dateFormat.format(new Date(iso)) : '—')

// Prefer the fresh detail, fall back to the row data while it loads.
const c = computed(() => (props.customer ? (props.detail?.id === props.customer.id ? props.detail : props.customer) : null))
const avgOrder = computed(() => (c.value && c.value.orders ? c.value.total_spent / c.value.orders : 0))

// Spend per category, from the recent orders the backend returns.
const byCategory = computed(() => {
  const totals = new Map<string, number>()
  for (const o of props.detail?.recent_orders ?? []) totals.set(o.category, (totals.get(o.category) ?? 0) + o.amount)
  const rows = [...totals].map(([category, amount]) => ({ category, amount })).sort((a, b) => b.amount - a.amount)
  const max = rows[0]?.amount ?? 0
  return rows.map((r) => ({ ...r, pct: max ? Math.round((r.amount / max) * 100) : 0 }))
})

// --- dialog focus management ---
const panel = ref<HTMLElement | null>(null)
const closeBtn = ref<HTMLButtonElement | null>(null)
let returnFocus: HTMLElement | null = null

watch(
  () => props.customer,
  async (now, before) => {
    if (now && !before) {
      returnFocus = document.activeElement as HTMLElement | null
      document.addEventListener('keydown', onKey)
      await nextTick()
      closeBtn.value?.focus()
    } else if (!now && before) {
      document.removeEventListener('keydown', onKey)
      returnFocus?.focus()
    }
  },
)
onUnmounted(() => document.removeEventListener('keydown', onKey))

function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    emit('close')
    return
  }
  if (e.key !== 'Tab' || !panel.value) return
  const focusable = panel.value.querySelectorAll<HTMLElement>('button, [href], [tabindex]:not([tabindex="-1"])')
  if (!focusable.length) return
  const first = focusable[0]!
  const last = focusable[focusable.length - 1]!
  if (e.shiftKey && document.activeElement === first) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && document.activeElement === last) {
    e.preventDefault()
    first.focus()
  }
}
</script>

<template>
  <Teleport to="body">
    <Transition name="drawer">
      <div v-if="c" class="overlay" @click.self="emit('close')">
        <aside ref="panel" class="drawer" role="dialog" aria-modal="true" aria-labelledby="customer-title">
          <header>
            <Avatar :name="`${c.first_name} ${c.last_name}`" :size="48" />
            <div class="title">
              <p class="eyebrow">Customer #{{ c.id }}</p>
              <h2 id="customer-title">{{ c.first_name }} {{ c.last_name }}</h2>
              <p class="muted">{{ c.city }} · Customer since {{ date(c.created_at) }}</p>
            </div>
            <button ref="closeBtn" type="button" class="close" aria-label="Close customer details" @click="emit('close')">
              <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" /></svg>
            </button>
          </header>

          <section aria-label="Contact and consent" class="block contact">
            <dl>
              <div>
                <dt>Email</dt>
                <dd>
                  {{ c.email }}
                  <span class="badge" :class="{ on: c.email_consent }">{{ c.email_consent ? 'Opted in' : 'Opted out' }}</span>
                </dd>
              </div>
              <div>
                <dt>Phone</dt>
                <dd>
                  {{ c.phone ?? 'Not provided' }}
                  <span class="badge" :class="{ on: c.sms_consent }">SMS {{ c.sms_consent ? 'opted in' : 'opted out' }}</span>
                </dd>
              </div>
              <div>
                <dt>Language</dt>
                <dd><LangChip :lang="c.language" /></dd>
              </div>
            </dl>
          </section>

          <dl class="stats" aria-label="Activity summary">
            <div>
              <dt>Orders</dt>
              <dd>{{ c.orders }}</dd>
            </div>
            <div>
              <dt>Lifetime value</dt>
              <dd>{{ c.orders ? money.format(c.total_spent) : '—' }}</dd>
            </div>
            <div>
              <dt>Avg. order</dt>
              <dd>{{ c.orders ? money.format(avgOrder) : '—' }}</dd>
            </div>
            <div>
              <dt>Last order</dt>
              <dd>{{ date(c.last_order_at) }}</dd>
            </div>
            <div>
              <dt>Last active</dt>
              <dd>{{ date(c.last_active_at) }}</dd>
            </div>
            <div>
              <dt>Last cart add</dt>
              <dd><template v-if="detail">{{ date(detail.last_cart_at) }}</template><span v-else class="skeleton-line inline" aria-label="Loading" /></dd>
            </div>
          </dl>

          <div v-if="error" class="alert" role="alert">
            <span>Couldn't load order history: {{ error }}</span>
            <button type="button" @click="emit('retry')">Retry</button>
          </div>
          <div v-else-if="loading && !detail" aria-label="Loading order history">
            <div v-for="n in 4" :key="n" class="skeleton" />
          </div>

          <template v-else-if="detail">
            <section v-if="byCategory.length" class="block" aria-labelledby="cat-title">
              <h3 id="cat-title">Spend by category <small>recent orders</small></h3>
              <ul class="bars">
                <li v-for="r in byCategory" :key="r.category">
                  <span class="cat">{{ r.category }}</span>
                  <span class="track" aria-hidden="true"><span :style="{ width: r.pct + '%' }" /></span>
                  <span class="amt">{{ money.format(r.amount) }}</span>
                </li>
              </ul>
            </section>

            <section class="block" aria-labelledby="orders-title">
              <h3 id="orders-title">
                Orders
                <small v-if="detail.orders > detail.recent_orders.length">latest {{ detail.recent_orders.length }} of {{ detail.orders }}</small>
              </h3>
              <table v-if="detail.recent_orders.length" class="mini">
                <thead>
                  <tr>
                    <th scope="col">Date</th>
                    <th scope="col">Category</th>
                    <th scope="col" class="num">Amount</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="o in detail.recent_orders" :key="o.id">
                    <td>{{ date(o.placed_at) }}</td>
                    <td class="cat">{{ o.category }}</td>
                    <td class="num">{{ money.format(o.amount) }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-else class="muted">No orders yet.</p>
            </section>

            <section class="block" aria-labelledby="carts-title">
              <h3 id="carts-title">
                Cart activity
                <small v-if="detail.carts > detail.recent_carts.length">latest {{ detail.recent_carts.length }} of {{ detail.carts }}</small>
              </h3>
              <ul v-if="detail.recent_carts.length" class="carts">
                <li v-for="a in detail.recent_carts" :key="a.id">
                  <span class="cat">{{ a.category }}</span>
                  <span class="muted">{{ date(a.added_at) }}</span>
                </li>
              </ul>
              <p v-else class="muted">No cart additions.</p>
            </section>
          </template>
        </aside>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.overlay {
  position: fixed;
  inset: 0;
  z-index: 50;
  display: flex;
  justify-content: flex-end;
  background: rgb(32 77 60 / 0.28);
}
.drawer {
  display: flex;
  flex-direction: column;
  gap: 16px;
  width: min(480px, 100%);
  height: 100%;
  padding: 24px;
  overflow-y: auto;
  background: var(--surface);
  box-shadow: -12px 0 40px rgb(32 77 60 / 0.2);
}
header {
  display: flex;
  align-items: flex-start;
  gap: 14px;
}
.title {
  flex: 1;
  min-width: 0;
}
.eyebrow {
  margin: 0;
  font-size: 11px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-muted);
}
h2 {
  margin: 0;
  font-size: 20px;
  line-height: 1.3;
}
h3 {
  margin: 0 0 8px;
  font-size: 14px;
}
h3 small {
  margin-left: 6px;
  font-weight: 500;
  color: var(--text-muted);
}
p {
  margin: 0;
}
.muted {
  color: var(--text-muted);
  font-size: 13px;
}
.avatar {
  flex: none;
  display: grid;
  place-items: center;
  width: 48px;
  height: 48px;
  border-radius: 50%;
  font-weight: 600;
  color: var(--accent);
  background: var(--accent-soft);
}
.close {
  display: grid;
  place-items: center;
  width: 36px;
  height: 36px;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--surface);
  color: var(--text);
  cursor: pointer;
}
.close svg {
  width: 16px;
  height: 16px;
}
.close:hover {
  background: var(--surface-muted);
}
.block {
  padding-top: 16px;
  border-top: 1px solid var(--border);
}
.contact dl {
  display: grid;
  gap: 10px;
  margin: 0;
}
dt {
  font-size: 12px;
  color: var(--text-muted);
}
dd {
  margin: 0;
  overflow-wrap: anywhere;
}
.upper {
  text-transform: uppercase;
}
.badge {
  display: inline-block;
  margin-left: 6px;
  padding: 1px 8px;
  border-radius: 99px;
  font-size: 11px;
  font-weight: 600;
  color: var(--text-muted);
  background: var(--surface-muted);
  border: 1px solid var(--border);
}
.badge.on {
  color: var(--success);
  background: var(--success-soft);
  border-color: transparent;
}
.stats {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1px;
  margin: 0;
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
  background: var(--border);
  overflow: hidden;
}
.stats div {
  padding: 10px 12px;
  background: var(--surface-muted);
}
.stats dd {
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.bars {
  display: grid;
  gap: 8px;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: 13px;
}
.bars li {
  display: grid;
  grid-template-columns: 96px 1fr auto;
  align-items: center;
  gap: 10px;
}
.track {
  height: 8px;
  border-radius: 99px;
  background: var(--surface-muted);
  overflow: hidden;
}
.track span {
  display: block;
  height: 100%;
  border-radius: 99px;
  background: var(--success);
}
.cat {
  text-transform: capitalize;
}
.amt,
.num {
  font-variant-numeric: tabular-nums;
  text-align: right;
}
.mini {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.mini th {
  padding: 6px 8px;
  text-align: left;
  font-size: 11px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  border-bottom: 1px solid var(--border);
}
.mini th.num {
  text-align: right;
}
.mini td {
  padding: 7px 8px;
  border-bottom: 1px solid var(--border);
}
.mini tr:last-child td {
  border-bottom: 0;
}
.carts {
  display: grid;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
  font-size: 13px;
}
.carts li {
  display: flex;
  justify-content: space-between;
}
.alert {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 14px;
  border-radius: var(--radius-control);
  color: var(--danger);
  background: var(--danger-soft);
}
.alert button {
  padding: 6px 12px;
  border: 1px solid currentColor;
  border-radius: var(--radius-control);
  background: none;
  color: inherit;
  cursor: pointer;
}
.skeleton {
  height: 40px;
  margin-bottom: 8px;
  border-radius: var(--radius-control);
  background: linear-gradient(90deg, var(--surface-muted), var(--border), var(--surface-muted));
  background-size: 200% 100%;
  animation: shimmer 1.4s infinite linear;
}
@keyframes shimmer {
  to {
    background-position: -200% 0;
  }
}
.drawer-enter-active,
.drawer-leave-active {
  transition: opacity 0.2s;
}
.drawer-enter-active .drawer,
.drawer-leave-active .drawer {
  transition: transform 0.25s ease;
}
.drawer-enter-from,
.drawer-leave-to {
  opacity: 0;
}
.drawer-enter-from .drawer,
.drawer-leave-to .drawer {
  transform: translateX(24px);
}
</style>
