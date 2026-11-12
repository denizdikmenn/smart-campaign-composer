<script setup lang="ts">
/**
 * Small inline SVG flag for a content language (no emoji: Windows has no flag
 * glyphs). English uses the UK flag; unknown codes get a neutral globe.
 * Decorative: the language name always appears next to it.
 */
defineProps<{ lang: string }>()
</script>

<template>
  <svg class="flag" viewBox="0 0 30 20" aria-hidden="true" focusable="false">
    <defs>
      <clipPath :id="`flag-clip-${lang}`"><rect width="30" height="20" rx="3" /></clipPath>
    </defs>
    <g :clip-path="`url(#flag-clip-${lang})`">
      <template v-if="lang === 'tr'">
        <rect width="30" height="20" fill="#e30a17" />
        <circle cx="11" cy="10" r="5" fill="#fff" />
        <circle cx="12.25" cy="10" r="4" fill="#e30a17" />
        <polygon fill="#fff" points="14.60,10.00 16.35,9.38 16.40,7.53 17.52,9.00 19.30,8.47 18.25,10.00 19.30,11.53 17.52,11.00 16.40,12.47 16.35,10.62" />
      </template>
      <template v-else-if="lang === 'en'">
        <rect width="30" height="20" fill="#012169" />
        <path d="M0 0l30 20M30 0L0 20" stroke="#fff" stroke-width="4" />
        <path d="M0 0l30 20M30 0L0 20" stroke="#c8102e" stroke-width="1.4" />
        <path d="M15 0v20M0 10h30" stroke="#fff" stroke-width="6" />
        <path d="M15 0v20M0 10h30" stroke="#c8102e" stroke-width="3.4" />
      </template>
      <template v-else-if="lang === 'de'">
        <rect width="30" height="7" fill="#000" /><rect y="6.67" width="30" height="7" fill="#dd0000" /><rect y="13.33" width="30" height="6.67" fill="#ffce00" />
      </template>
      <template v-else-if="lang === 'fr'">
        <rect width="10" height="20" fill="#0055a4" /><rect x="10" width="10" height="20" fill="#fff" /><rect x="20" width="10" height="20" fill="#ef4135" />
      </template>
      <template v-else-if="lang === 'it'">
        <rect width="10" height="20" fill="#009246" /><rect x="10" width="10" height="20" fill="#fff" /><rect x="20" width="10" height="20" fill="#ce2b37" />
      </template>
      <template v-else-if="lang === 'es'">
        <rect width="30" height="20" fill="#aa151b" /><rect y="5" width="30" height="10" fill="#f1bf00" />
      </template>
      <template v-else-if="lang === 'ru'">
        <rect width="30" height="7" fill="#fff" /><rect y="6.67" width="30" height="7" fill="#0039a6" /><rect y="13.33" width="30" height="6.67" fill="#d52b1e" />
      </template>
      <template v-else-if="lang === 'nl'">
        <rect width="30" height="7" fill="#ae1c28" /><rect y="6.67" width="30" height="7" fill="#fff" /><rect y="13.33" width="30" height="6.67" fill="#21468b" />
      </template>
      <template v-else>
        <rect width="30" height="20" fill="#dde8b5" />
        <g fill="none" stroke="#204d3c" stroke-width="1.3">
          <circle cx="15" cy="10" r="6.5" /><ellipse cx="15" cy="10" rx="2.8" ry="6.5" /><path d="M8.5 10h13" />
        </g>
      </template>
    </g>
    <rect width="30" height="20" rx="3" fill="none" stroke="rgb(0 0 0 / .12)" />
  </svg>
</template>

<style scoped>
.flag {
  flex: none;
  width: 18px;
  height: 12px;
  border-radius: 3px;
  vertical-align: -1px;
}
</style>
