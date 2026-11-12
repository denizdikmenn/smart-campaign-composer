<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref } from 'vue'
import {
  fetchAudiences,
  fetchCustomer,
  fetchCustomers,
  fetchSummary,
  type AudienceReport,
  type ConsentFilter,
  type Customer,
  type CustomerDetail,
  type SortField,
  type SortOrder,
  type Summary,
} from '../api'
import AudienceCard from '../components/AudienceCard.vue'
import CustomerDrawer from '../components/CustomerDrawer.vue'
import CustomerHoverCard from '../components/CustomerHoverCard.vue'
import CustomerTable from '../components/CustomerTable.vue'
import MetricCard from '../components/MetricCard.vue'

const PAGE_SIZE = 25
const number = new Intl.NumberFormat('en-US')
const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'TRY', maximumFractionDigits: 0 })
const message = (e: unknown) => (e instanceof Error ? e.message : 'Something went wrong')
const isAbort = (e: unknown) => e instanceof DOMException && e.name === 'AbortError'

// --- summary -------------------------------------------------------------
const summary = ref<Summary | null>(null)
const summaryError = ref('')

async function loadSummary() {
  summaryError.value = ''
  try {
    summary.value = await fetchSummary()
  } catch (e) {
    summaryError.value = message(e)
  }
}

const pct = (part: number, whole: number) => (whole ? Math.round((part / whole) * 100) : 0)

// --- audiences -----------------------------------------------------------
const report = ref<AudienceReport | null>(null)
const audienceError = ref('')
const segment = ref('') // selected audience id, '' = everyone
const category = ref('') // category used by the category-buyers audience
const customersPanel = ref<HTMLElement | null>(null)
const flash = ref(false) // briefly highlights the table after an audience is picked

async function loadAudiences() {
  audienceError.value = ''
  try {
    report.value = await fetchAudiences(category.value)
    category.value = report.value.category
  } catch (e) {
    audienceError.value = message(e)
  }
}

async function changeCategory(value: string) {
  category.value = value
  await loadAudiences()
  if (segment.value === 'category_buyers') applyFilters()
}

async function selectAudience(id: string) {
  segment.value = segment.value === id ? '' : id
  applyFilters()
  if (!segment.value) return
  // Show the effect of the click: bring the filtered table into view.
  await nextTick()
  const reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches
  customersPanel.value?.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'start' })
  flash.value = false
  requestAnimationFrame(() => (flash.value = true))
}

const selectedAudience = computed(() => report.value?.audiences.find((a) => a.id === segment.value))

// --- customer list -------------------------------------------------------
const filters = reactive<{ city: string; email: ConsentFilter; sms: ConsentFilter }>({
  city: '',
  email: '',
  sms: '',
})
const sort = reactive<{ field: SortField | ''; order: SortOrder }>({ field: '', order: 'asc' })
const page = ref(1)
const customers = ref<Customer[]>([])
const total = ref(0)
const loading = ref(true)
const loaded = ref(false) // true after the first successful response
const error = ref('')

let controller: AbortController | undefined

async function load() {
  controller?.abort() // ignore stale responses
  controller = new AbortController()
  loading.value = true
  error.value = ''
  hideHover()
  try {
    const res = await fetchCustomers(
      {
        page: page.value,
        pageSize: PAGE_SIZE,
        city: filters.city,
        emailConsent: filters.email,
        smsConsent: filters.sms,
        segment: segment.value,
        category: category.value,
        sort: sort.field,
        order: sort.order,
      },
      controller.signal,
    )
    customers.value = res.items
    total.value = res.total
    loaded.value = true
    loading.value = false
  } catch (e) {
    if (isAbort(e)) return
    error.value = message(e)
    loading.value = false
  }
}

function applyFilters() {
  page.value = 1
  load()
}

function setSort(field: SortField, order: SortOrder) {
  sort.field = field
  sort.order = order
  applyFilters()
}

function resetFilters() {
  filters.city = ''
  filters.email = ''
  filters.sms = ''
  segment.value = ''
  applyFilters()
}

function goTo(p: number) {
  page.value = p
  load()
}

const hasFilters = computed(() => Boolean(filters.city || filters.email || filters.sms || segment.value))
const pageCount = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))
const rangeStart = computed(() => (total.value === 0 ? 0 : (page.value - 1) * PAGE_SIZE + 1))
const rangeEnd = computed(() => Math.min(page.value * PAGE_SIZE, total.value))

// --- customer detail (shared by hover preview and drawer) ----------------
const details = new Map<number, CustomerDetail>() // small per-session cache
const pending = new Map<number, Promise<CustomerDetail>>()

function getDetail(id: number): Promise<CustomerDetail> {
  const cached = details.get(id)
  if (cached) return Promise.resolve(cached)
  let p = pending.get(id)
  if (!p) {
    p = fetchCustomer(id)
      .then((d) => {
        details.set(id, d)
        return d
      })
      .finally(() => pending.delete(id))
    pending.set(id, p)
  }
  return p
}

// Hover preview: only on devices with a real pointer, after a short delay.
const canHover = window.matchMedia('(hover: hover) and (pointer: fine)').matches
const HOVER_DELAY = 450
const hover = ref<{ customer: Customer; anchor: DOMRect; detail: CustomerDetail | null } | null>(null)
let hoverTimer: ReturnType<typeof setTimeout> | undefined

function onHover(c: Customer | null, row: HTMLElement | null) {
  clearTimeout(hoverTimer)
  if (!c || !row || !canHover || drawerFor.value) {
    hover.value = null
    return
  }
  hoverTimer = setTimeout(() => {
    hover.value = { customer: c, anchor: row.getBoundingClientRect(), detail: details.get(c.id) ?? null }
    if (!hover.value.detail) {
      getDetail(c.id)
        .then((d) => {
          if (hover.value?.customer.id === c.id) hover.value.detail = d
        })
        .catch(() => {}) // the drawer shows errors; the preview just stays partial
    }
  }, HOVER_DELAY)
}

function hideHover() {
  clearTimeout(hoverTimer)
  hover.value = null
}

// Drawer
const drawerFor = ref<Customer | null>(null)
const drawerDetail = ref<CustomerDetail | null>(null)
const drawerLoading = ref(false)
const drawerError = ref('')

async function openCustomer(c: Customer) {
  hideHover()
  drawerFor.value = c
  drawerDetail.value = details.get(c.id) ?? null
  drawerError.value = ''
  if (drawerDetail.value) return
  drawerLoading.value = true
  try {
    const d = await getDetail(c.id)
    if (drawerFor.value?.id === c.id) drawerDetail.value = d
  } catch (e) {
    if (drawerFor.value?.id === c.id) drawerError.value = message(e)
  } finally {
    drawerLoading.value = false
  }
}

function closeCustomer() {
  drawerFor.value = null
  drawerDetail.value = null
}

onMounted(() => {
  loadSummary()
  loadAudiences()
  load()
  window.addEventListener('scroll', hideHover, { passive: true, capture: true })
})
onUnmounted(() => {
  controller?.abort()
  clearTimeout(hoverTimer)
  window.removeEventListener('scroll', hideHover, { capture: true })
})
</script>

<template>
  <div>
    <header class="page-head">
      <h1>Audience Explorer</h1>
      <p>Browse your customers, preset audiences and channel reach.</p>
    </header>

    <h2>Overview</h2>
    <section class="grid" aria-label="Overview">
      <template v-if="summary">
        <MetricCard label="Total customers" :value="number.format(summary.total)" :hint="`Across ${summary.cities.length} cities`" />
        <MetricCard
          label="Buyers"
          :value="number.format(summary.buyers)"
          :percent="pct(summary.buyers, summary.total)"
          :hint="`${pct(summary.buyers, summary.total)}% of customers have ordered`"
        />
        <MetricCard
          label="Repeat buyers"
          :value="number.format(summary.repeat_buyers)"
          :percent="pct(summary.repeat_buyers, summary.buyers)"
          :hint="`${pct(summary.repeat_buyers, summary.buyers)}% of buyers ordered 2+ times`"
        />
        <MetricCard label="Avg. order value" :value="money.format(summary.avg_order_value)" hint="Across all orders" />
        <MetricCard
          label="Active, last 30 days"
          :value="number.format(summary.active_30d)"
          :percent="pct(summary.active_30d, summary.total)"
          :hint="`${pct(summary.active_30d, summary.total)}% of customers`"
        />
        <MetricCard
          label="Email consent"
          :value="number.format(summary.email_consent)"
          :percent="pct(summary.email_consent, summary.total)"
          :hint="`${pct(summary.email_consent, summary.total)}% of customers`"
        />
        <MetricCard
          label="SMS consent"
          :value="number.format(summary.sms_consent)"
          :percent="pct(summary.sms_consent, summary.total)"
          :hint="`${pct(summary.sms_consent, summary.total)}% of customers`"
        />
      </template>
      <div v-else-if="summaryError" class="alert" role="alert">
        <span>Couldn't load metrics: {{ summaryError }}</span>
        <button type="button" @click="loadSummary">Retry</button>
      </div>
      <template v-else>
        <div v-for="n in 7" :key="n" class="skeleton card-skeleton" />
      </template>
    </section>

    <h2>Audiences</h2>
    <p class="lead">
      <svg viewBox="0 0 16 16" aria-hidden="true"><path d="M8 1.5v2M8 12.5v2M1.5 8h2M12.5 8h2M3.4 3.4l1.4 1.4M11.2 11.2l1.4 1.4M3.4 12.6l1.4-1.4M11.2 4.8l1.4-1.4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" /></svg>
      Pick an audience to filter the customer table below. Click it again to clear.
    </p>
    <section class="grid audiences" aria-label="Audiences">
      <template v-if="report">
        <AudienceCard
          v-for="(a, i) in report.audiences"
          :key="a.id"
          :audience="a"
          :index="i"
          :selected="segment === a.id"
          :categories="a.id === 'category_buyers' ? report.categories : undefined"
          :category="category"
          @select="selectAudience(a.id)"
          @update:category="changeCategory"
        />
      </template>
      <div v-else-if="audienceError" class="alert" role="alert">
        <span>Couldn't load audiences: {{ audienceError }}</span>
        <button type="button" @click="loadAudiences">Retry</button>
      </div>
      <template v-else>
        <div v-for="n in 6" :key="n" class="skeleton card-skeleton tall" />
      </template>
    </section>

    <h2 id="customers-title">Customers</h2>
    <section
      ref="customersPanel"
      class="panel"
      :class="{ flash }"
      aria-labelledby="customers-title"
      @animationend="flash = false"
    >
      <div class="filters">
        <label>
          City
          <select v-model="filters.city" @change="applyFilters">
            <option value="">All cities</option>
            <option v-for="c in summary?.cities ?? []" :key="c" :value="c">{{ c }}</option>
          </select>
        </label>
        <label>
          Email consent
          <select v-model="filters.email" @change="applyFilters">
            <option value="">Any</option>
            <option value="true">Opted in</option>
            <option value="false">Opted out</option>
          </select>
        </label>
        <label>
          SMS consent
          <select v-model="filters.sms" @change="applyFilters">
            <option value="">Any</option>
            <option value="true">Opted in</option>
            <option value="false">Opted out</option>
          </select>
        </label>
        <span v-if="selectedAudience" class="chip">
          Audience: {{ selectedAudience.name }}
          <button aria-label="Remove audience filter" @click="selectAudience(selectedAudience.id)">×</button>
        </span>
        <button class="link" :disabled="!hasFilters" @click="resetFilters">Clear filters</button>
      </div>

      <div v-if="error" class="state" role="alert">
        <p class="title">Couldn't load customers</p>
        <p>{{ error }}</p>
        <button @click="load">Try again</button>
      </div>

      <div v-else-if="!loaded" class="skeletons" aria-label="Loading customers">
        <div v-for="n in 8" :key="n" class="skeleton row-skeleton" />
      </div>

      <div v-else-if="customers.length === 0" class="state">
        <p class="title">No customers found</p>
        <p>No one matches the current filters.</p>
        <button v-if="hasFilters" @click="resetFilters">Clear filters</button>
      </div>

      <template v-else>
        <div class="scroll">
          <CustomerTable
            :customers="customers"
            :busy="loading"
            :sort="sort.field"
            :order="sort.order"
            :active-id="drawerFor?.id ?? null"
            @sort="setSort"
            @open="openCustomer"
            @hover="onHover"
          />
        </div>
        <footer>
          <span role="status">
            Showing {{ number.format(rangeStart) }}–{{ number.format(rangeEnd) }} of {{ number.format(total) }}
          </span>
          <div class="pager">
            <button :disabled="page <= 1 || loading" @click="goTo(page - 1)">Previous</button>
            <span>Page {{ page }} of {{ number.format(pageCount) }}</span>
            <button :disabled="page >= pageCount || loading" @click="goTo(page + 1)">Next</button>
          </div>
        </footer>
      </template>
    </section>

    <CustomerHoverCard v-if="hover" :customer="hover.customer" :detail="hover.detail" :anchor="hover.anchor" />
    <CustomerDrawer
      :customer="drawerFor"
      :detail="drawerDetail"
      :loading="drawerLoading"
      :error="drawerError"
      @close="closeCustomer"
      @retry="drawerFor && openCustomer(drawerFor)"
    />
  </div>
</template>

<style scoped>
.page-head {
  margin-bottom: 8px;
}
h1 {
  margin: 0;
  font-size: 28px;
  line-height: 1.3;
  letter-spacing: -0.02em;
}
.page-head p {
  margin: 2px 0 0;
  color: var(--text-muted);
}
h2 {
  margin: 28px 0 12px;
  font-size: 16px;
  letter-spacing: -0.01em;
  scroll-margin-top: 16px;
}
.lead {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: -4px 0 12px;
  font-size: 13px;
  color: var(--text-muted);
}
.lead svg {
  width: 16px;
  height: 16px;
  flex: none;
  color: var(--accent);
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(220px, 100%), 1fr));
  gap: 16px;
}
.audiences {
  grid-template-columns: repeat(auto-fit, minmax(min(300px, 100%), 1fr));
}

.panel {
  scroll-margin-top: 16px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius);
  box-shadow: var(--shadow);
  overflow: hidden;
}
.panel.flash {
  animation: flash 1.2s ease-out;
}
@keyframes flash {
  from {
    box-shadow: 0 0 0 4px var(--accent-soft), var(--shadow);
  }
}
.filters {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: 12px 16px;
  padding: 16px;
}
.filters label {
  display: grid;
  gap: 4px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
}
select {
  min-width: 160px;
  padding: 8px 10px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
}
.chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 6px 6px 12px;
  border-radius: 99px;
  font-size: 13px;
  font-weight: 600;
  color: var(--accent);
  background: var(--accent-soft);
}
.chip button {
  padding: 0 8px;
  border: 0;
  border-radius: 99px;
  background: none;
  font-size: 16px;
  line-height: 1.4;
  color: inherit;
}

button {
  padding: 8px 14px;
  border: 1px solid var(--border);
  border-radius: 10px;
  background: var(--surface);
  cursor: pointer;
}
button:hover:not(:disabled) {
  background: var(--surface-muted);
}
button:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
button.link {
  margin-left: auto;
  border-color: transparent;
  background: none;
  color: var(--accent);
  font-weight: 600;
}

/* Bounded scroll area: lets the header and first column stay sticky. */
.scroll {
  max-height: min(70vh, 760px);
  overflow: auto;
  border-top: 1px solid var(--border);
  overscroll-behavior: contain;
}
footer {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 12px 16px;
  border-top: 1px solid var(--border);
  font-size: 13px;
  color: var(--text-muted);
}
.pager {
  display: flex;
  align-items: center;
  gap: 12px;
}

.state {
  display: grid;
  justify-items: center;
  gap: 4px;
  padding: 56px 16px;
  border-top: 1px solid var(--border);
  text-align: center;
  color: var(--text-muted);
}
.state p {
  margin: 0;
}
.state .title {
  font-size: 16px;
  font-weight: 600;
  color: var(--text);
}
.state button {
  margin-top: 12px;
}

.alert {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 14px 16px;
  border-radius: var(--radius);
  color: var(--danger);
  background: var(--danger-soft);
}

.skeletons {
  display: grid;
  gap: 1px;
  border-top: 1px solid var(--border);
}
.skeleton {
  background: linear-gradient(90deg, var(--surface-muted) 25%, var(--border) 50%, var(--surface-muted) 75%);
  background-size: 200% 100%;
  animation: shimmer 1.4s infinite linear;
}
.row-skeleton {
  height: 61px;
}
.card-skeleton {
  height: 118px;
  border-radius: var(--radius);
}
.card-skeleton.tall {
  height: 170px;
}
@keyframes shimmer {
  to {
    background-position: -200% 0;
  }
}
@media (max-width: 760px) {
  .scroll {
    max-height: none;
  }
}
</style>
