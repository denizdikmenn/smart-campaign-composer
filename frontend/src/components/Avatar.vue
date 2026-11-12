<script setup lang="ts">
import { computed } from 'vue'

/** Initials avatar; the tint is stable per person and drawn from the theme's greens. */
const props = withDefaults(defineProps<{ name: string; size?: number }>(), { size: 36 })

const TINTS = [
  ['#dde8b5', '#204d3c'],
  ['#e3f1ea', '#287d60'],
  ['#e6eedc', '#3f6b45'],
  ['#f1ecd8', '#6b5a24'],
  ['#e1ece9', '#2c5f5a'],
  ['#ece6f1', '#5a4470'],
]
const initials = computed(
  () =>
    props.name
      .split(/\s+/)
      .filter(Boolean)
      .slice(0, 2)
      .map((p) => p[0]!.toLocaleUpperCase('tr'))
      .join('') || '?',
)
const tint = computed(() => {
  let h = 0
  for (const ch of props.name) h = (h * 31 + ch.charCodeAt(0)) >>> 0
  return TINTS[h % TINTS.length]!
})
</script>

<template>
  <span
    class="avatar"
    aria-hidden="true"
    :style="{ width: `${size}px`, height: `${size}px`, fontSize: `${Math.round(size * 0.38)}px`, background: tint[0], color: tint[1] }"
  >
    {{ initials }}
  </span>
</template>

<style scoped>
.avatar {
  flex: none;
  display: inline-grid;
  place-items: center;
  font-weight: 600;
  letter-spacing: 0.02em;
  border-radius: 50%;
  box-shadow: inset 0 0 0 1px rgb(32 77 60 / 0.08);
}
</style>
