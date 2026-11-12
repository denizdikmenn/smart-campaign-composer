import { computed, ref } from 'vue'

/**
 * Minimal hash router: a few routes don't justify vue-router (and a new
 * dependency would need reinstalling node_modules in the dev container).
 * Dynamic routes: an unfinished campaign, #/campaigns/{id}, and a campaign's
 * delivery page, #/campaigns/{id}/delivery.
 */
type StaticRoute = '/campaigns' | '/campaigns/new' | '/explorer'
export type Route = StaticRoute | `/campaigns/${number}` | `/campaigns/${number}/delivery`

const ROUTES: StaticRoute[] = ['/campaigns', '/campaigns/new', '/explorer']
const DEFAULT: Route = '/campaigns'
const DELIVERY = /^\/campaigns\/(\d+)\/delivery$/
const EDIT = /^\/campaigns\/(\d+)$/

function parse(): Route {
  const path = window.location.hash.replace(/^#/, '')
  if (DELIVERY.test(path) || EDIT.test(path)) return path as Route
  return (ROUTES as string[]).includes(path) ? (path as Route) : DEFAULT
}

export const route = ref<Route>(parse())

/** Campaign id of the delivery route, or null on other pages. */
export const deliveryId = computed(() => {
  const m = DELIVERY.exec(route.value)
  return m ? Number(m[1]) : null
})

/** Campaign id of an unfinished campaign being edited, or null. */
export const editId = computed(() => {
  const m = EDIT.exec(route.value)
  return m ? Number(m[1]) : null
})
export const editHref = (id: number) => href(`/campaigns/${id}`)

window.addEventListener('hashchange', () => {
  route.value = parse()
  window.scrollTo({ top: 0 })
})

export const href = (r: Route) => `#${r}`
export const deliveryHref = (id: number) => href(`/campaigns/${id}/delivery`)

export function navigate(r: Route) {
  window.location.hash = r
}
