<script setup lang="ts">
import { ref, computed } from "vue"
import { useRoute, useRouter } from "vue-router"
import { HomeOutline, SettingsOutline } from "@vicons/ionicons5"

const route = useRoute()
const router = useRouter()
const visible = ref(false)

const hiddenRoutes = new Set(["host-room", "joined-room", "pc-host-room", "pc-joined-room"])
const shouldHide = computed(() => hiddenRoutes.has(route.name as string))

const navItems = [
  { name: "home", icon: HomeOutline, path: "/" },
  { name: "settings", icon: SettingsOutline, path: "/settings" },
] as const

function isActive(name: string) {
  return route.name === name
}

function navigate(path: string) {
  router.push(path)
}
</script>

<template>
  <!-- Trigger zone -->
  <div
    v-if="!shouldHide"
    class="fixed bottom-0 inset-x-0 h-1.5 z-40"
    @mouseenter="visible = true"
  />

  <!-- Floating nav bar -->
  <nav
    v-if="!shouldHide"
    class="fixed bottom-3 left-1/2 z-50 flex h-12 items-center justify-center gap-1 rounded-full border border-border bg-background px-4 shadow-[0_4px_24px_oklch(0_0_0/0.1)] transition-all duration-250 ease-[cubic-bezier(0.4,0,0.2,1)]"
    :style="{ transform: `translateX(-50%) translateY(${visible ? '0' : '100%'})`, pointerEvents: visible ? 'auto' : 'none' }"
    @mouseleave="visible = false"
  >
    <button
      v-for="item in navItems"
      :key="item.name"
      class="nav-btn"
      :class="isActive(item.name) && 'nav-btn--active'"
      @click="navigate(item.path)"
    >
      <span class="nav-btn__hover" />
      <component :is="item.icon" class="size-5" />
    </button>

    <!-- Avatar -->
    <button class="nav-btn" :class="isActive('user') && 'nav-btn--active'" @click="navigate('/user')">
      <span class="nav-btn__hover" />
      <div class="nav-avatar">
        <svg viewBox="0 0 24 24" class="size-full">
          <circle cx="12" cy="8" r="4" fill="currentColor" opacity="0.5" />
          <circle cx="12" cy="12" r="10" fill="none" />
          <path d="M4 20c0-4 3.6-7 8-7s8 3 8 7" fill="currentColor" opacity="0.3" />
        </svg>
      </div>
    </button>
  </nav>
</template>

<style scoped>
.nav-btn {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2.5rem;
  height: 2.5rem;
  border: none;
  background: transparent;
  color: oklch(0.556 0 0);
  cursor: pointer;
  outline: none;
}

.nav-btn__hover {
  position: absolute;
  z-index: 0;
  width: 2rem;
  height: 2rem;
  border-radius: 9999px;
  background: oklch(0 0 0 / 0%);
  transition: transform 0.2s cubic-bezier(0.4, 0, 0.2, 1), background 0.15s ease;
  transform: scale(0);
  pointer-events: none;
}

.nav-btn :deep(svg) {
  position: relative;
  z-index: 1;
}

.nav-btn:hover .nav-btn__hover {
  background: oklch(0 0 0 / 8%);
  transform: scale(1);
}

.nav-btn:active .nav-btn__hover {
  background: oklch(0 0 0 / 14%);
  transform: scale(1.15);
}

.nav-btn--active {
  color: oklch(0.205 0 0);
}

.nav-btn--active .nav-btn__hover {
  background: oklch(0 0 0 / 8%);
  transform: scale(1);
}

.nav-avatar {
  position: relative;
  z-index: 1;
  width: 1.5rem;
  height: 1.5rem;
  border-radius: 9999px;
  overflow: hidden;
  background: oklch(0.87 0 0);
  color: oklch(0.556 0 0);
}
</style>
