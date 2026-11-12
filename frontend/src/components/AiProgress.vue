<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'

/**
 * Shared "work in progress" card: animated orb, title, optional stage list
 * (done / current / upcoming), a progress bar and elapsed time.
 * `progress` (0..1) gives a determinate bar; omit it for an indeterminate one.
 * Only the title line is live, so stage changes don't flood screen readers.
 */
const props = withDefaults(
  defineProps<{
    title: string
    detail?: string
    stages?: string[]
    current?: number
    progress?: number
    /** 'ai' shows the sparkle orb (model work); 'task' a plain one (loading, counting). */
    kind?: 'ai' | 'task'
    compact?: boolean
    elapsed?: boolean
  }>(),
  { detail: '', stages: () => [], current: 0, progress: undefined, kind: 'ai', compact: false, elapsed: true },
)

const seconds = ref(0)
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => (timer = setInterval(() => seconds.value++, 1000)))
onUnmounted(() => clearInterval(timer))

const pct = computed(() => (props.progress == null ? null : Math.round(Math.min(1, Math.max(0, props.progress)) * 100)))
const state = (i: number) => (i < props.current ? 'done' : i === props.current ? 'current' : 'todo')
const time = computed(() => (seconds.value < 60 ? `${seconds.value}s` : `${Math.floor(seconds.value / 60)}m ${seconds.value % 60}s`))
</script>

<template>
  <div class="ai-progress" :class="[kind, { compact }]" aria-busy="true">
    <span class="orb" aria-hidden="true">
      <svg v-if="kind === 'ai'" viewBox="0 0 24 24">
        <path d="M12 3l1.9 5.6L19.5 10.5l-5.6 1.9L12 18l-1.9-5.6L4.5 10.5l5.6-1.9Z" />
        <path class="twinkle" d="M19 15l.8 2.2L22 18l-2.2.8L19 21l-.8-2.2L16 18l2.2-.8Z" />
      </svg>
      <span v-else class="ring" />
    </span>

    <div class="body">
      <p class="title" role="status" aria-live="polite">
        {{ title }}<span class="dots" aria-hidden="true"><i /><i /><i /></span>
      </p>
      <p v-if="detail" class="detail">{{ detail }}</p>

      <ol v-if="stages.length" class="stages">
        <li v-for="(s, i) in stages" :key="s" :class="state(i)">
          <span class="mark" aria-hidden="true">
            <svg v-if="state(i) === 'done'" viewBox="0 0 16 16"><path d="M4 8.5l2.5 2.5L12 5.5" /></svg>
          </span>
          {{ s }}
          <span class="sr-only">{{ state(i) === 'done' ? '(done)' : state(i) === 'current' ? '(in progress)' : '' }}</span>
        </li>
      </ol>

      <div
        class="bar"
        :class="{ indeterminate: pct == null }"
        role="progressbar"
        :aria-label="title"
        aria-valuemin="0"
        aria-valuemax="100"
        :aria-valuenow="pct ?? undefined"
      >
        <span :style="pct == null ? undefined : { width: `${pct}%` }" />
      </div>
    </div>

    <div class="side">
      <span v-if="elapsed" class="time" aria-hidden="true">{{ time }}</span>
      <slot />
    </div>
  </div>
</template>

<style scoped>
.ai-progress {
  --glow: rgb(203 222 150 / 0.55);
  position: relative;
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: start;
  gap: 14px;
  padding: 16px 18px;
  overflow: hidden;
  border: 1px solid #cfdcb4;
  border-radius: var(--radius);
  background:
    radial-gradient(120% 140% at 0% 0%, rgb(221 232 181 / 0.55), transparent 55%),
    linear-gradient(var(--surface), var(--surface));
  box-shadow: var(--shadow);
  animation: appear 0.3s ease-out backwards;
}
.ai-progress.task {
  border-color: var(--border);
  background: var(--surface);
}
.ai-progress.compact {
  padding: 12px 14px;
  gap: 12px;
  box-shadow: none;
}

/* Orb */
.orb {
  position: relative;
  display: grid;
  place-items: center;
  width: 40px;
  height: 40px;
  border-radius: 50%;
  color: var(--accent);
  background: conic-gradient(from 0deg, #dde8b5, #9cbf86, #204d3c, #9cbf86, #dde8b5);
  animation: spin 3.2s linear infinite;
}
.orb::before {
  content: '';
  position: absolute;
  inset: 3px;
  border-radius: 50%;
  background: var(--surface);
}
.orb::after {
  content: '';
  position: absolute;
  inset: -6px;
  z-index: -1;
  border-radius: 50%;
  background: var(--glow);
  filter: blur(8px);
  animation: breathe 2.4s ease-in-out infinite;
}
.orb svg {
  position: relative;
  width: 20px;
  height: 20px;
  fill: currentColor;
  animation: spin 3.2s linear infinite reverse; /* keeps the sparkle upright */
}
.orb .twinkle {
  transform-origin: 19px 18px;
  animation: twinkle 1.6s ease-in-out infinite;
}
.task .orb {
  background: none;
  animation: none;
}
.task .orb::before,
.task .orb::after {
  display: none;
}
.ring {
  width: 26px;
  height: 26px;
  border: 3px solid var(--accent-soft);
  border-top-color: var(--accent);
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}
.compact .orb {
  width: 32px;
  height: 32px;
}
.compact .orb svg {
  width: 16px;
  height: 16px;
}

/* Text */
.body {
  display: grid;
  gap: 6px;
  min-width: 0;
}
.title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--text);
}
.compact .title {
  font-size: 14px;
}
.detail {
  margin: 0;
  font-size: 12px;
  color: var(--text-muted);
}
.dots {
  display: inline-flex;
  gap: 3px;
  margin-left: 5px;
  vertical-align: middle;
}
.dots i {
  width: 4px;
  height: 4px;
  border-radius: 50%;
  background: currentColor;
  animation: bounce 1.2s ease-in-out infinite;
}
.dots i:nth-child(2) {
  animation-delay: 0.15s;
}
.dots i:nth-child(3) {
  animation-delay: 0.3s;
}

/* Stages */
.stages {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 4px 0 2px;
  padding: 0;
  list-style: none;
}
.stages li {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 3px 10px 3px 4px;
  font-size: 12px;
  font-weight: 500;
  border-radius: 99px;
  color: var(--text-muted);
  background: var(--surface-muted);
  border: 1px solid var(--border);
  transition:
    background-color 0.3s,
    color 0.3s,
    border-color 0.3s;
}
.mark {
  display: grid;
  place-items: center;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  border: 1.5px dashed #b8c7b3;
}
.mark svg {
  width: 12px;
  height: 12px;
  fill: none;
  stroke: #fff;
  stroke-width: 2.2;
  stroke-linecap: round;
  stroke-linejoin: round;
}
.stages li.done {
  color: var(--success);
  background: var(--success-soft);
  border-color: transparent;
}
.stages li.done .mark {
  border: 0;
  background: var(--success);
  animation: pop 0.3s ease-out;
}
.stages li.current {
  color: var(--accent);
  font-weight: 600;
  background: var(--selected-surface);
  border-color: #b9cc86;
}
.stages li.current .mark {
  border: 2px solid var(--accent-soft);
  border-top-color: var(--accent);
  animation: spin 0.8s linear infinite;
}

/* Bar */
.bar {
  position: relative;
  height: 6px;
  margin-top: 4px;
  overflow: hidden;
  border-radius: 99px;
  background: var(--surface-muted);
  box-shadow: inset 0 0 0 1px var(--border);
}
.bar span {
  position: absolute;
  inset: 0 auto 0 0;
  border-radius: inherit;
  background: linear-gradient(90deg, #9cbf86, var(--accent));
  transition: width 0.6s ease;
}
.bar span::after {
  content: '';
  position: absolute;
  inset: 0;
  background: linear-gradient(90deg, transparent, rgb(255 255 255 / 0.55), transparent);
  background-size: 50% 100%;
  background-repeat: no-repeat;
  animation: glint 1.6s linear infinite;
}
.bar.indeterminate span {
  width: 35%;
  animation: slide 1.4s ease-in-out infinite;
}

.side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
}
.time {
  font-size: 12px;
  font-variant-numeric: tabular-nums;
  color: var(--text-muted);
}

@keyframes appear {
  from {
    opacity: 0;
    transform: translateY(4px);
  }
}
@keyframes breathe {
  50% {
    opacity: 0.35;
    transform: scale(0.85);
  }
}
@keyframes twinkle {
  50% {
    opacity: 0.3;
    transform: scale(0.6);
  }
}
@keyframes bounce {
  0%,
  80%,
  100% {
    opacity: 0.3;
    transform: translateY(0);
  }
  40% {
    opacity: 1;
    transform: translateY(-3px);
  }
}
@keyframes pop {
  from {
    transform: scale(0.4);
  }
}
@keyframes glint {
  from {
    background-position: -100% 0;
  }
  to {
    background-position: 200% 0;
  }
}
@keyframes slide {
  from {
    left: -35%;
  }
  to {
    left: 100%;
  }
}
@media (prefers-reduced-motion: reduce) {
  .bar.indeterminate span {
    left: 0;
    width: 100%;
    opacity: 0.35;
  }
}
@media (max-width: 560px) {
  .ai-progress {
    grid-template-columns: auto minmax(0, 1fr);
  }
  .side {
    grid-column: 1 / -1;
    flex-direction: row;
    justify-content: space-between;
    align-items: center;
  }
}
</style>
