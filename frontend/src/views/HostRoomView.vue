<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useScaffoldingStore } from '@/stores/scaffolding'
import { useWatermarkStore } from '@/stores/watermark'
import { Button } from '@/components/ui/button'
import { useClipboard } from '@vueuse/core'
import { CopyOutline, StopCircleOutline, CheckmarkOutline } from '@vicons/ionicons5'
import WatermarkShare from '@/components/WatermarkShare.vue'

const router = useRouter()
const scaffold = useScaffoldingStore()
const watermark = useWatermarkStore()
const { copy, copied } = useClipboard()
let pollTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  pollTimer = setInterval(() => scaffold.refreshRoomStatus(), 3000)
  watermark.loadDemoImages()
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})

async function handleStop() {
  await scaffold.stopRoom()
  router.push('/')
}

function copyCode() {
  if (scaffold.roomStatus?.code) {
    copy(scaffold.roomStatus.code)
  }
}
</script>

<template>
  <div class="flex flex-1 flex-col items-center justify-center gap-6 px-6">
    <!-- Room Code Card -->
    <div v-if="scaffold.roomStatus" class="w-full max-w-sm space-y-6">
      <!-- Room Code -->
      <div class="rounded-xl border border-border bg-card p-5 text-center space-y-3">
        <p class="text-xs text-muted-foreground uppercase tracking-wider">房间代码</p>
        <p class="font-mono text-2xl font-bold tracking-widest break-all">
          {{ scaffold.roomStatus.code }}
        </p>
        <Button variant="ghost" size="sm" @click="copyCode" class="gap-1.5">
          <component :is="copied ? CheckmarkOutline : CopyOutline" class="size-3.5" />
          <span class="text-xs">{{ copied ? '已复制' : '复制代码' }}</span>
        </Button>
      </div>

      <!-- Player List -->
      <div class="rounded-xl border border-border bg-card p-4 space-y-3">
        <div class="flex items-center justify-between">
          <p class="text-xs text-muted-foreground uppercase tracking-wider">在线玩家</p>
          <span class="text-xs text-muted-foreground">{{ scaffold.roomStatus.online_count ?? 0 }} 人</span>
        </div>

        <div v-if="!scaffold.roomStatus.players || scaffold.roomStatus.players.length === 0" class="py-4 text-center text-sm text-muted-foreground">
          等待玩家加入...
        </div>

        <ul v-else class="space-y-2">
          <li
            v-for="player in scaffold.roomStatus.players"
            :key="player.machine_id"
            class="flex items-center gap-3 rounded-lg px-3 py-2 bg-muted/50"
          >
            <div class="flex size-8 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">
              {{ player.name.charAt(0).toUpperCase() }}
            </div>
            <div class="flex-1 min-w-0">
              <p class="text-sm font-medium truncate">{{ player.name }}</p>
              <p class="text-xs text-muted-foreground">{{ player.kind === 'HOST' ? '房主' : '玩家' }}</p>
            </div>
          </li>
        </ul>
      </div>

      <!-- Watermark Share -->
      <WatermarkShare
        v-if="scaffold.roomStatus?.code"
        :room-code="scaffold.roomStatus.code"
      />

      <!-- Stop Button -->
      <Button variant="destructive" class="w-full gap-2" @click="handleStop">
        <StopCircleOutline class="size-4" />
        停止房间
      </Button>
    </div>

    <!-- Loading State -->
    <div v-else class="flex flex-col items-center gap-3">
      <div class="size-8 animate-spin rounded-full border-2 border-primary border-t-transparent" />
      <p class="text-sm text-muted-foreground">正在创建房间...</p>
    </div>
  </div>
</template>
