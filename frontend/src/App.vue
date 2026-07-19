<script setup lang="ts">
import TitleBar from "@/components/TitleBar.vue"
import BottomNav from "@/components/BottomNav.vue"
import { useUserStore } from "@/stores/user"
import { useSettingsStore } from "@/stores/settings"
import { useDownloadStore } from "@/stores/download"
import { useGlobalDrop } from "@/composables/useGlobalDrop"
import { Button } from "@/components/ui/button"
import { formatSize, formatSpeed } from "@/lib/utils"
import { onMounted, onUnmounted, computed } from "vue"

const user = useUserStore()
user.refreshUser()

const settings = useSettingsStore()
settings.loadPeers()

const download = useDownloadStore()
onMounted(() => download.startListening())
onUnmounted(() => download.stopListening())

const { showDropOverlay, dropStatus, dropRoomCode, dropError, cancel } = useGlobalDrop()

const progressLabel = computed(() => {
  if (!download.progress) return ''
  const p = download.progress
  if (p.step === 'downloading') {
    const parts = [`下载中 ${p.percent}%`]
    if (p.totalSize > 0) parts.push(formatSize(p.totalSize))
    if (p.speed > 0) parts.push(formatSpeed(p.speed))
    return parts.join(' · ')
  }
  if (p.step === 'extracting') {
    return `解压中 ${p.percent}%`
  }
  return ''
})
</script>

<template>
  <div class="flex h-screen flex-col overflow-hidden">
    <TitleBar />
    <main class="flex flex-1 flex-col overflow-auto">
      <RouterView />
    </main>
    <BottomNav />

    <Transition name="overlay-fade">
      <div
        v-if="download.status === 'downloading' || download.status === 'extracting'"
        class="fixed inset-0 z-50 flex items-center justify-center backdrop-blur-xl bg-background/60"
      >
        <div class="flex flex-col items-center gap-4 px-10 py-10 rounded-2xl border border-border bg-card/90 min-w-72">
          <div class="size-10 animate-spin rounded-full border-2 border-primary border-t-transparent" />
          <p class="text-lg font-semibold">正在下载 EasyTier</p>
          <p class="text-sm text-muted-foreground">{{ progressLabel }}</p>
        </div>
      </div>
    </Transition>

    <Transition name="overlay-fade">
      <div
        v-if="download.status === 'error'"
        class="fixed inset-0 z-50 flex items-center justify-center backdrop-blur-xl bg-background/60"
      >
        <div class="flex flex-col items-center gap-4 px-8 py-10 rounded-2xl border border-destructive/30 bg-card/90">
          <div class="flex size-12 items-center justify-center rounded-full bg-destructive/10">
            <svg class="size-6 text-destructive" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </div>
          <p class="text-lg font-semibold text-destructive">EasyTier 下载失败</p>
          <p class="max-w-xs text-center text-sm text-muted-foreground">{{ download.errorMessage }}</p>
          <div class="mt-2 flex gap-3">
            <Button size="sm" @click="download.retry">重试</Button>
            <Button variant="outline" size="sm" @click="download.dismiss">关闭</Button>
          </div>
        </div>
      </div>
    </Transition>

    <!-- Global Drop Overlay -->
    <Transition name="overlay-fade">
      <div
        v-if="showDropOverlay"
        class="fixed inset-0 z-50 flex items-center justify-center backdrop-blur-xl bg-background/60"
        @click.self="dropStatus === 'idle' && cancel()"
      >
        <!-- Idle: dragging, waiting for drop -->
        <div
          v-if="dropStatus === 'idle'"
          class="flex flex-col items-center gap-4 px-8 py-12 rounded-2xl border-2 border-dashed border-primary/50 bg-card/90"
        >
          <div class="flex size-16 items-center justify-center rounded-full bg-primary/10">
            <svg class="size-8 text-primary" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
              <path stroke-linecap="round" stroke-linejoin="round" d="M12 16V4m0 0L8 8m4-4l4 4m-4 8v4m0 0a4 4 0 01-4-4m4 4a4 4 0 004-4" />
            </svg>
          </div>
          <p class="text-lg font-semibold">松开以加入房间</p>
          <p class="text-sm text-muted-foreground">将联机图片拖入窗口即可自动加入房间</p>
        </div>

        <!-- Joining -->
        <div
          v-else-if="dropStatus === 'joining'"
          class="flex flex-col items-center gap-4 px-10 py-10 rounded-2xl border border-border bg-card/90 min-w-72"
        >
          <div class="size-10 animate-spin rounded-full border-2 border-primary border-t-transparent" />
          <p class="text-lg font-semibold">正在加入房间</p>
          <p
            v-if="dropRoomCode"
            class="font-mono text-xl font-bold tracking-widest text-primary"
          >
            {{ dropRoomCode }}
          </p>
          <Button variant="ghost" size="sm" class="mt-2" @click="cancel">
            取消
          </Button>
        </div>

        <!-- Error -->
        <div
          v-else-if="dropStatus === 'error'"
          class="flex flex-col items-center gap-4 px-8 py-10 rounded-2xl border border-destructive/30 bg-card/90"
        >
          <div class="flex size-12 items-center justify-center rounded-full bg-destructive/10">
            <svg class="size-6 text-destructive" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
            </svg>
          </div>
          <p class="text-lg font-semibold text-destructive">加入失败</p>
          <p class="text-sm text-muted-foreground text-center max-w-xs">{{ dropError }}</p>
          <Button variant="outline" size="sm" class="mt-2" @click="cancel">
            关闭
          </Button>
        </div>
      </div>
    </Transition>
  </div>
</template>

<style scoped>
.overlay-fade-enter-active {
  transition: opacity 0.15s ease-out;
}
.overlay-fade-leave-active {
  transition: opacity 0.2s ease-in;
}
.overlay-fade-enter-from,
.overlay-fade-leave-to {
  opacity: 0;
}
</style>
