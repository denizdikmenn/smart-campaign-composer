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

// --- AI audience suggestions ------------------------------------------------

export interface AudienceCounts {
  total: number
  email_eligible: number
  sms_eligible: number
}

export interface AudienceAlternative {
  id: string
  name: string
  reasoning: string
  primary: boolean
  match: 'all' | 'any'
  conditions: unknown[]
  conditions_text: string[]
  counts: AudienceCounts
  /** Valid rules, but no customer matches today. */
  empty: boolean
}

export type SuggestionOutcome = 'supported' | 'needs_clarification' | 'unsupported' | 'invalid' 

export interface SuggestionResponse {
  source: 'claude' | 'mock'
  model?: string
  outcome: SuggestionOutcome
  message: string
  questions: string[]
  unsupported_requirements: string[]
  suggestions: string[]
  total_customers: number
  alternatives: AudienceAlternative[]
  warnings: string[]
}

/** Error from a POST that may carry validation warnings from the backend. */
export class SuggestError extends Error {
  warnings: string[]
  constructor(message: string, warnings: string[] = []) {
    super(message)
    this.warnings = warnings
  }
}

export async function suggestAudiences(brief: string, signal?: AbortSignal): Promise<SuggestionResponse> {
  const res = await fetch('/api/segmentation/suggest', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ brief }),
    signal,
  })
  if (!res.ok) {
    let message = `Request failed (${res.status})`
    let warnings: string[] = []
    try {
      const body: { error?: string; warnings?: string[] } = await res.json()
      if (body.error) message = body.error
      warnings = body.warnings ?? []
    } catch {
      // keep the generic message
    }
    throw new SuggestError(message, warnings)
  }
  return res.json() as Promise<SuggestionResponse>
}

export interface PreviewCustomer {
  first_name: string
  last_name: string
  email: string
  city: string
  email_consent: boolean
  sms_consent: boolean
  orders: number
  total_spent: number
}

export async function previewCustomers(a: AudienceAlternative, signal?: AbortSignal): Promise<PreviewCustomer[]> {
  const res = await fetch('/api/segmentation/preview', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ match: a.match, conditions: a.conditions }),
    signal,
  })
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
  return ((await res.json()) as { customers: PreviewCustomer[] }).customers
}
