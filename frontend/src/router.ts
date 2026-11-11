import { ref } from 'vue'

/**
 * Minimal hash router: three static routes don't justify vue-router (and a new
 * dependency would need reinstalling node_modules in the dev container).
 */
export type Route = '/campaigns' | '/campaigns/new' | '/explorer'

const ROUTES: Route[] = ['/campaigns', '/campaigns/new', '/explorer']
const DEFAULT: Route = '/campaigns'

function parse(): Route {
  const path = window.location.hash.replace(/^#/, '')
  return (ROUTES as string[]).includes(path) ? (path as Route) : DEFAULT
}

export const route = ref<Route>(parse())

window.addEventListener('hashchange', () => {
  route.value = parse()
  window.scrollTo({ top: 0 })
})

export const href = (r: Route) => `#${r}`

export function navigate(r: Route) {
  window.location.hash = r
}
