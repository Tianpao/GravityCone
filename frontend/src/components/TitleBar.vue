<script setup lang="ts">
import { ref } from "vue"
import { Window as thisWindow } from "@wailsio/runtime"
import { RemoveOutline, CloseOutline, PinOutline } from "@vicons/ionicons5"

const isAlwaysOnTop = ref(false)

async function handleMinimise() {
  await thisWindow.Minimise()
}

async function handleAlwaysOnTop() {
  isAlwaysOnTop.value = !isAlwaysOnTop.value
  await thisWindow.SetAlwaysOnTop(isAlwaysOnTop.value)
}

async function handleClose() {
  await thisWindow.Close()
}
</script>

<template>
    <header
      class="flex h-9 shrink-0 items-center bg-background select-none border-b border-border"
      style="--wails-draggable: drag"
    >
      <!-- Left: Title -->
      <div class="flex items-center pl-3">
        <span class="text-xs font-medium text-muted-foreground">GravityCone</span>
      </div>

      <!-- Spacer to push controls right -->
      <div class="flex-1" />

      <!-- Right: Window controls -->
      <div class="flex items-center" style="--wails-draggable: no-drag" @mousedown.stop>
        <button
          class="titlebar-btn"
          :class="isAlwaysOnTop && 'titlebar-btn--active'"
          @click="handleAlwaysOnTop"
        >
          <span class="titlebar-btn__hover" />
          <PinOutline class="size-3.5" />
        </button>

        <button
          class="titlebar-btn"
          @click="handleMinimise"
        >
          <span class="titlebar-btn__hover" />
          <RemoveOutline class="size-3.5" />
        </button>

        <button
          class="titlebar-btn titlebar-btn--close"
          @click="handleClose"
        >
          <span class="titlebar-btn__hover" />
          <CloseOutline class="size-3.5" />
        </button>
      </div>
    </header>
</template>

<style scoped>
.titlebar-btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.5rem;
  height: 2.25rem;
  border: none;
  background: transparent;
  color: inherit;
  cursor: pointer;
  outline: none;
  -webkit-app-region: no-drag;
}

.titlebar-btn__hover {
  position: absolute;
  z-index: 0;
  width: 1.5rem;
  height: 1.5rem;
  border-radius: 9999px;
  background: oklch(0 0 0 / 0%);
  transition: transform 0.2s cubic-bezier(0.4, 0, 0.2, 1), background 0.15s ease;
  transform: scale(0);
  pointer-events: none;
}

.titlebar-btn :deep(svg) {
  position: relative;
  z-index: 1;
}

.titlebar-btn:hover .titlebar-btn__hover {
  background: oklch(0 0 0 / 8%);
  transform: scale(1);
}

.titlebar-btn:active .titlebar-btn__hover {
  background: oklch(0 0 0 / 14%);
  transform: scale(1.15);
}

.titlebar-btn--active .titlebar-btn__hover {
  background: oklch(0 0 0 / 8%);
  transform: scale(1);
}

.titlebar-btn--close:hover .titlebar-btn__hover {
  background: oklch(0.577 0.245 27.325 / 90%);
}

.titlebar-btn--close:hover {
  color: oklch(0.985 0 0);
}

.titlebar-btn:active:not(.titlebar-btn--close) .titlebar-btn__hover {
  background: oklch(0 0 0 / 14%);
}

.titlebar-btn:active.titlebar-btn--close .titlebar-btn__hover {
  background: oklch(0.577 0.245 27.325 / 80%);
}
</style>
