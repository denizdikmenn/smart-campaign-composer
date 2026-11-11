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
}

async function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  const res = await fetch(path, { signal })
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
  return get<CustomerPage>(`/api/customers?${params}`, signal)
}

export function fetchAudiences(category: string, signal?: AbortSignal) {
  const params = new URLSearchParams()
  if (category) params.set('category', category)
  return get<AudienceReport>(`/api/audiences?${params}`, signal)
}
