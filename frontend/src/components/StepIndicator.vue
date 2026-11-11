<script setup lang="ts">
import { computed } from 'vue'

/**
 * Campaign progress across the planned stages (DESIGN.md §2; REQUIREMENTS.md
 * workflow). Stages that aren't built are shown as "Coming soon" and are never
 * clickable, so nothing looks usable that isn't.
 */
interface Step {
  label: string
  /** Short description of what happens in the stage. */
  hint: string
  available: boolean
}

const props = defineProps<{
  steps: Step[]
  /** Index of the stage in progress. */
  current: number
  /** 0..1 progress inside the current stage (fills the connector to the next stage). */
  progress: number
  /** Live status of the current stage, e.g. "Compare options". */
  status: string
}>()

const clamp = (n: number) => Math.min(1, Math.max(0, n))
// Overall fill of the track, from the first node to the last.
const fill = computed(() => {
  const segments = Math.max(1, props.steps.length - 1)
  return clamp((props.current + clamp(props.progress)) / segments) * 100
})
const state = (i: number) =>
  i < props.current || (i === props.current && props.progress >= 1) ? 'done' : i === props.current ? 'current' : 'todo'
</script>

<template>
  <nav class="stepper" aria-label="Campaign progress">
    <!-- Compact version for narrow screens. -->
    <p class="compact" aria-hidden="true">
      <span class="compact-step">Step {{ current + 1 }} of {{ steps.length }}</span>
      <strong>{{ steps[current]?.label }}</strong>
      <span class="compact-status">· {{ status }}</span>
    </p>

    <div class="rail" aria-hidden="true">
      <span class="rail-fill" :style="{ width: fill + '%' }" />
    </div>

    <ol>
      <li
        v-for="(s, i) in steps"
        :key="s.label"
        :class="[state(i), { soon: !s.available }]"
        :aria-current="state(i) === 'current' ? 'step' : undefined"
      >
        <span class="node" aria-hidden="true">
          <svg v-if="state(i) === 'done'" viewBox="0 0 16 16">
            <path d="m4 8.5 2.5 2.5L12 5.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          <svg v-else-if="!s.available" viewBox="0 0 16 16">
            <rect x="4" y="7" width="8" height="6" rx="1.5" fill="none" stroke="currentColor" stroke-width="1.5" />
            <path d="M5.8 7V5.6a2.2 2.2 0 0 1 4.4 0V7" fill="none" stroke="currentColor" stroke-width="1.5" />
          </svg>
          <template v-else>{{ i + 1 }}</template>
        </span>
        <span class="text">
          <span class="label">
            {{ s.label }}
            <span class="sr-only">
              {{ state(i) === 'done' ? '(completed)' : state(i) === 'current' ? `(current: ${status})` : s.available ? '' : '(not available yet)' }}
            </span>
          </span>
          <span v-if="state(i) === 'current'" class="status" aria-hidden="true">{{ status }}</span>
          <span v-else-if="!s.available" class="tag" aria-hidden="true">Coming soon</span>
          <span v-else class="hint">{{ s.hint }}</span>
        </span>
      </li>
    </ol>
  </nav>
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
/* Sticky top bar: the one place DESIGN.md allows glass, with an opaque fallback. */
.stepper {
  --node: 34px;
  position: sticky;
  top: 12px;
  z-index: 10;
  margin-bottom: 24px;
  padding: 16px 20px 14px;
  border: 1px solid var(--border);
  border-radius: var(--radius);
  background: var(--surface);
  box-shadow: 0 8px 24px rgb(32 77 60 / 0.06);
}
@supports (backdrop-filter: blur(1px)) {
  .stepper {
    background: rgb(255 255 255 / 0.8);
    backdrop-filter: blur(14px) saturate(1.2);
    -webkit-backdrop-filter: blur(14px) saturate(1.2);
  }
}

/* Track behind the nodes: nodes are centred in 4 equal columns (at 1/8, 3/8, 5/8, 7/8),
   so the rail runs from the first centre to the last and segments are equal. */
.rail {
  --pad: 20px;
  position: absolute;
  top: calc(16px + var(--node) / 2 - 2px);
  left: calc(var(--pad) + (100% - 2 * var(--pad)) / 8);
  right: calc(var(--pad) + (100% - 2 * var(--pad)) / 8);
  height: 4px;
  border-radius: 99px;
  background: var(--border);
}
.rail-fill {
  display: block;
  height: 100%;
  border-radius: inherit;
  background: linear-gradient(90deg, #9cbf86, var(--accent));
  transition: width 0.6s cubic-bezier(0.2, 0.7, 0.2, 1);
}

ol {
  position: relative;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  margin: 0;
  padding: 0;
  list-style: none;
}
li {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  min-width: 0;
  padding: 0 6px;
  text-align: center;
}
.node {
  display: grid;
  place-items: center;
  width: var(--node);
  height: var(--node);
  border-radius: 50%;
  font-size: 13px;
  font-weight: 600;
  color: var(--text-muted);
  background: var(--surface);
  border: 2px solid var(--border);
  transition:
    background-color 0.3s,
    border-color 0.3s,
    color 0.3s,
    box-shadow 0.3s;
}
.node svg {
  width: 16px;
  height: 16px;
}
.current .node {
  color: var(--accent);
  border-color: var(--accent);
  background: var(--accent-soft);
  box-shadow: 0 0 0 5px rgb(221 232 181 / 0.6);
  animation: halo 0.8s ease-out 1;
}
.done .node {
  color: #fff;
  border-color: var(--accent);
  background: var(--accent);
}
.soon .node {
  background: var(--surface-muted);
}

.text {
  display: grid;
  gap: 1px;
}
.label {
  font-size: 14px;
  font-weight: 600;
  color: var(--text);
}
.todo .label {
  color: var(--text-muted);
}
.status {
  font-size: 12px;
  font-weight: 500;
  color: var(--success);
}
.status::before {
  content: '';
  display: inline-block;
  width: 6px;
  height: 6px;
  margin-right: 6px;
  vertical-align: middle;
  border-radius: 50%;
  background: currentColor;
}
.hint {
  font-size: 12px;
  color: var(--text-muted);
}
.tag {
  justify-self: center;
  width: fit-content;
  padding: 1px 8px;
  font-size: 11px;
  font-weight: 600;
  color: var(--text-muted);
  border: 1px solid var(--border);
  border-radius: 99px;
  background: var(--surface-muted);
}

.compact {
  display: none;
  margin: 0 0 10px;
  font-size: 13px;
}
.compact-step {
  margin-right: 6px;
  color: var(--text-muted);
}
.compact-status {
  color: var(--success);
}

@keyframes halo {
  from {
    box-shadow: 0 0 0 14px rgb(221 232 181 / 0);
  }
  40% {
    box-shadow: 0 0 0 9px rgb(221 232 181 / 0.9);
  }
}

/* Narrow: one summary line + the progress rail + nodes only. */
@media (max-width: 720px) {
  .stepper {
    --node: 28px;
    padding: 12px 16px;
  }
  .compact {
    display: block;
  }
  .rail {
    --pad: 16px;
    top: calc(12px + 30px + var(--node) / 2 - 2px);
  }
  .text {
    display: none;
  }
  li {
    gap: 0;
  }
}
</style>
