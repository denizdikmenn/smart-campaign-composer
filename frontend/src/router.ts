import { computed, ref } from 'vue'

/**
 * Minimal hash router: a few routes don't justify vue-router (and a new
 * dependency would need reinstalling node_modules in the dev container).
 * One dynamic route: a campaign's delivery page, #/campaigns/{id}/delivery.
 */
type StaticRoute = '/campaigns' | '/campaigns/new' | '/explorer'
export type Route = StaticRoute | `/campaigns/${number}/delivery`

const ROUTES: StaticRoute[] = ['/campaigns', '/campaigns/new', '/explorer']
const DEFAULT: Route = '/campaigns'
const DELIVERY = /^\/campaigns\/(\d+)\/delivery$/

function parse(): Route {
  const path = window.location.hash.replace(/^#/, '')
  if (DELIVERY.test(path)) return path as Route
  return (ROUTES as string[]).includes(path) ? (path as Route) : DEFAULT
}

export const route = ref<Route>(parse())

/** Campaign id of the delivery route, or null on other pages. */
export const deliveryId = computed(() => {
  const m = DELIVERY.exec(route.value)
  return m ? Number(m[1]) : null
})

window.addEventListener('hashchange', () => {
  route.value = parse()
  window.scrollTo({ top: 0 })
})

export const href = (r: Route) => `#${r}`
export const deliveryHref = (id: number) => href(`/campaigns/${id}/delivery`)

export function navigate(r: Route) {
  window.location.hash = r
}
