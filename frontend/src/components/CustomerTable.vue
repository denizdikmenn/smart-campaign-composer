<script setup lang="ts">
import type { Customer, SortField, SortOrder } from '../api'

defineProps<{
  customers: Customer[]
  busy?: boolean
  sort: SortField | ''
  order: SortOrder
  /** Row currently open in the detail drawer. */
  activeId?: number | null
}>()

const emit = defineEmits<{
  sort: [field: SortField, order: SortOrder]
  open: [customer: Customer]
  hover: [customer: Customer | null, row: HTMLElement | null]
}>()

interface Column {
  label: string
  field?: SortField
  num?: boolean
  /** Numbers and dates start descending (biggest/newest first), text ascending. */
  desc?: boolean
}

const COLUMNS: Column[] = [
  { label: 'Customer', field: 'name' },
  { label: 'City', field: 'city' },
  { label: 'Lang', field: 'language' },
  { label: 'Orders', field: 'orders', num: true, desc: true },
  { label: 'Lifetime value', field: 'total_spent', num: true, desc: true },
  { label: 'Last order', field: 'last_order_at', desc: true },
  { label: 'Last active', field: 'last_active_at', desc: true },
  { label: 'Email' },
  { label: 'SMS' },
]

const dateFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })
const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'TRY', maximumFractionDigits: 0 })
const date = (iso: string | null) => (iso ? dateFormat.format(new Date(iso)) : '—')
const initials = (c: Customer) => `${c.first_name.charAt(0)}${c.last_name.charAt(0)}`.toUpperCase()

function toggle(col: Column, current: SortField | '', order: SortOrder) {
  if (!col.field) return
  if (current === col.field) emit('sort', col.field, order === 'asc' ? 'desc' : 'asc')
  else emit('sort', col.field, col.desc ? 'desc' : 'asc')
}

const ariaSort = (col: Column, current: SortField | '', order: SortOrder) =>
  col.field && current === col.field ? (order === 'asc' ? 'ascending' : 'descending') : undefined
</script>

<template>
  <table :class="{ busy }" :aria-busy="busy">
    <caption class="sr-only">
      Customers. Column headers sort the list; select a row to open customer details.
    </caption>
    <thead>
      <tr>
        <th
          v-for="col in COLUMNS"
          :key="col.label"
          scope="col"
          :class="{ num: col.num, first: col.field === 'name' }"
          :aria-sort="ariaSort(col, sort, order)"
        >
          <button v-if="col.field" type="button" class="sorter" :class="{ on: sort === col.field }" @click="toggle(col, sort, order)">
            {{ col.label }}
            <svg viewBox="0 0 12 12" aria-hidden="true" :class="sort === col.field ? order : 'none'">
              <path class="up" d="M6 2 9 5H3z" />
              <path class="down" d="M6 10 3 7h6z" />
            </svg>
          </button>
          <template v-else>{{ col.label }}</template>
        </th>
        <th scope="col" class="go"><span class="sr-only">Open</span></th>
      </tr>
    </thead>
    <tbody>
      <tr
        v-for="c in customers"
        :key="c.id"
        tabindex="0"
        :class="{ active: activeId === c.id }"
        :aria-label="`${c.first_name} ${c.last_name}, open details`"
        @click="emit('open', c)"
        @keydown.enter.prevent="emit('open', c)"
        @keydown.space.prevent="emit('open', c)"
        @mouseenter="emit('hover', c, $event.currentTarget as HTMLElement)"
        @mouseleave="emit('hover', null, null)"
      >
        <td class="first">
          <div class="who">
            <span class="avatar" aria-hidden="true">{{ initials(c) }}</span>
            <span class="names">
              <strong>{{ c.first_name }} {{ c.last_name }}</strong>
              <small>{{ c.email }}</small>
            </span>
          </div>
        </td>
        <td data-label="City">{{ c.city }}</td>
        <td data-label="Lang"><span class="lang">{{ c.language }}</span></td>
        <td data-label="Orders" class="num">{{ c.orders }}</td>
        <td data-label="Lifetime value" class="num">{{ c.orders ? money.format(c.total_spent) : '—' }}</td>
        <td data-label="Last order">{{ date(c.last_order_at) }}</td>
        <td data-label="Last active">{{ date(c.last_active_at) }}</td>
        <td data-label="Email">
          <span class="badge" :class="{ on: c.email_consent }">{{ c.email_consent ? 'Opted in' : 'Opted out' }}</span>
        </td>
        <td data-label="SMS">
          <span class="badge" :class="{ on: c.sms_consent }">{{ c.sms_consent ? 'Opted in' : 'Opted out' }}</span>
        </td>
        <td class="go" aria-hidden="true">
          <svg viewBox="0 0 16 16"><path d="m6 3 5 5-5 5" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
        </td>
      </tr>
    </tbody>
  </table>
</template>

<style scoped>
.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip: rect(0 0 0 0);
  white-space: nowrap;
}
table {
  width: 100%;
  border-collapse: separate;
  border-spacing: 0;
  transition: opacity 0.15s;
}
table.busy {
  opacity: 0.55;
}
th {
  position: sticky;
  top: 0;
  z-index: 2;
  padding: 0 16px;
  height: 44px;
  text-align: left;
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  background: var(--surface-muted);
  border-bottom: 1px solid var(--border);
  white-space: nowrap;
}
th.num {
  text-align: right;
}
.sorter {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  margin: 0 -6px;
  padding: 6px;
  border: 0;
  border-radius: 6px;
  background: none;
  font: inherit;
  letter-spacing: inherit;
  text-transform: inherit;
  color: inherit;
  cursor: pointer;
}
.sorter:hover,
.sorter.on {
  color: var(--text);
}
.sorter:hover {
  background: var(--border);
}
.sorter svg {
  width: 10px;
  height: 10px;
  fill: currentColor;
}
.sorter svg.none path {
  opacity: 0.3;
}
.sorter svg.asc .down,
.sorter svg.desc .up {
  opacity: 0.2;
}
td {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border);
  vertical-align: middle;
  white-space: nowrap;
  background: var(--surface);
  transition: background-color 0.15s;
}
tbody tr {
  cursor: pointer;
}
tbody tr:hover td,
tbody tr:focus-visible td {
  background: var(--surface-muted);
}
tbody tr.active td {
  background: var(--selected-surface);
}
tbody tr:focus-visible {
  outline: 2px solid var(--accent);
  outline-offset: -2px;
}
tbody tr:last-child td {
  border-bottom: 0;
}
/* Customer column stays visible while scrolling sideways. */
.first {
  position: sticky;
  left: 0;
  z-index: 1;
  box-shadow: 1px 0 0 var(--border);
}
th.first {
  z-index: 3;
}
.who {
  display: flex;
  align-items: center;
  gap: 12px;
}
.names {
  display: grid;
}
.names small {
  color: var(--text-muted);
}
.avatar {
  flex: none;
  width: 36px;
  height: 36px;
  border-radius: 50%;
  display: grid;
  place-items: center;
  font-size: 13px;
  font-weight: 600;
  color: var(--accent);
  background: var(--accent-soft);
}
.num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.lang {
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  color: var(--text-muted);
}
.badge {
  display: inline-block;
  padding: 2px 10px;
  border-radius: 99px;
  font-size: 12px;
  font-weight: 600;
  color: var(--text-muted);
  background: var(--surface-muted);
}
.badge.on {
  color: var(--success);
  background: var(--success-soft);
}
.go {
  width: 40px;
  padding-left: 0;
  color: var(--text-muted);
}
.go svg {
  display: block;
  width: 16px;
  height: 16px;
  transition: transform 0.15s;
}
tbody tr:hover .go svg {
  transform: translateX(2px);
  color: var(--text);
}

@media (max-width: 760px) {
  thead {
    display: none;
  }
  tbody tr {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px 16px;
    padding: 14px 16px;
    border-bottom: 1px solid var(--border);
  }
  td {
    padding: 0;
    border: 0;
    background: none !important;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  td.first {
    position: static;
    grid-column: 1 / -1;
    box-shadow: none;
  }
  td.num {
    text-align: left;
  }
  td.go {
    display: none;
  }
  tbody tr:hover,
  tbody tr.active {
    background: var(--surface-muted);
  }
  td[data-label]::before {
    content: attr(data-label);
    display: block;
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-muted);
  }
}
</style>
