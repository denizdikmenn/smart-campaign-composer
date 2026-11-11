<script setup lang="ts">
import type { Customer } from '../api'

defineProps<{ customers: Customer[]; busy?: boolean }>()

const dateFormat = new Intl.DateTimeFormat('en-GB', { dateStyle: 'medium' })
const money = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'TRY', maximumFractionDigits: 0 })
const date = (iso: string | null) => (iso ? dateFormat.format(new Date(iso)) : '—')

function initials(c: Customer) {
  return `${c.first_name.charAt(0)}${c.last_name.charAt(0)}`.toUpperCase()
}
</script>

<template>
  <table :class="{ busy }" :aria-busy="busy">
    <thead>
      <tr>
        <th>Customer</th>
        <th>City</th>
        <th>Lang</th>
        <th class="num">Orders</th>
        <th class="num">Lifetime value</th>
        <th>Last order</th>
        <th>Last active</th>
        <th>Email</th>
        <th>SMS</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="c in customers" :key="c.id">
        <td class="who">
          <span class="avatar" aria-hidden="true">{{ initials(c) }}</span>
          <span>
            <strong>{{ c.first_name }} {{ c.last_name }}</strong>
            <small>{{ c.email }}</small>
          </span>
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
      </tr>
    </tbody>
  </table>
</template>

<style scoped>
table {
  width: 100%;
  border-collapse: collapse;
  transition: opacity 0.15s;
}
table.busy {
  opacity: 0.5;
}
th {
  padding: 10px 16px;
  text-align: left;
  font-size: 12px;
  font-weight: 600;
  text-transform: uppercase;
  letter-spacing: 0.04em;
  color: var(--text-muted);
  background: var(--surface-muted);
  white-space: nowrap;
}
td {
  padding: 12px 16px;
  border-top: 1px solid var(--border);
  vertical-align: middle;
  white-space: nowrap;
}
tbody tr:hover {
  background: var(--surface-muted);
}
.who {
  display: flex;
  align-items: center;
  gap: 12px;
}
.who small {
  display: block;
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
  font-weight: 700;
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

@media (max-width: 760px) {
  thead {
    display: none;
  }
  tr {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 10px 16px;
    padding: 14px 16px;
    border-top: 1px solid var(--border);
  }
  td {
    padding: 0;
    border: 0;
    white-space: normal;
    overflow-wrap: anywhere;
  }
  td.who {
    grid-column: 1 / -1;
  }
  td.num {
    text-align: left;
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
