<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePaperConnectStore } from '@/stores/paperconnect'
import { useWatermarkStore } from '@/stores/watermark'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { StopCircleOutline, CopyOutline, CheckmarkOutline } from '@vicons/ionicons5'
import WatermarkShare from '@/components/WatermarkShare.vue'

const pcStore = usePaperConnectStore()
const watermark = useWatermarkStore()
const router = useRouter()
const copied = ref(false)
const showStopDialog = ref(false)
let pollTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  pollTimer = setInterval(() => {
    pcStore.pcRefreshRoomStatus()
  }, 3000)
  watermark.loadDemoImages()
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})

watch(() => pcStore.pcRoomStatus, (status) => {
  if (!status?.running && pcStore.pcHostError) {
    showStopDialog.value = true
  }
})

function copyCode() {
  const code = pcStore.hostRoomCodePc
  if (!code) return
  navigator.clipboard.writeText(code)
  copied.value = true
  setTimeout(() => { copied.value = false }, 2000)
}

async function handleStop() {
  await pcStore.pcStopRoom()
  router.push('/')
}

const players = () => pcStore.pcRoomStatus?.players ?? []
</script>

<template>
  <div class="flex flex-col items-center justify-center flex-1 p-6">
    <template v-if="!pcStore.pcRoomStatus?.running">
      <div class="flex flex-col items-center gap-3">
        <div class="size-8 animate-spin rounded-full border-2 border-primary border-t-transparent"></div>
        <p class="text-muted-foreground">正在创建房间...</p>
      </div>
    </template>

    <template v-else>
      <div class="w-full max-w-sm space-y-6">
        <!-- Room code card -->
        <div class="rounded-xl border border-border bg-card p-5 text-center">
          <p class="text-xs uppercase text-muted-foreground mb-2">房间代码</p>
          <p class="font-mono text-2xl font-bold tracking-widest break-all">{{ pcStore.hostRoomCodePc }}</p>
          <Button variant="ghost" size="sm" class="mt-2" @click="copyCode">
            <component :is="copied ? CheckmarkOutline : CopyOutline" class="size-4 mr-1" />
            {{ copied ? '已复制' : '复制联机码' }}
          </Button>
        </div>

        <!-- Host hint -->
        <div class="rounded-xl border border-primary/20 bg-primary/5 p-3">
          <p class="text-sm text-muted-foreground">请确保 Minecraft 基岩版世界已开启局域网联机，GravityCone 会自动代理连接</p>
        </div>

        <!-- Player list -->
        <div class="rounded-xl border border-border bg-card p-4 space-y-3">
          <div class="flex items-center justify-between">
            <p class="text-sm font-medium">在线玩家</p>
            <span class="text-xs text-muted-foreground">{{ pcStore.pcRoomStatus?.online_count ?? 0 }} 人</span>
          </div>

          <div v-if="players().length === 0" class="text-sm text-muted-foreground text-center py-2">
            等待玩家加入...
          </div>

          <ul v-else class="space-y-2">
            <li v-for="player in players()" :key="player.player"
                class="flex items-center gap-3 rounded-lg px-3 py-2 bg-muted/50">
              <div class="flex size-8 items-center justify-center rounded-full bg-primary/10 text-xs font-medium text-primary">
                {{ player.player.charAt(0).toUpperCase() }}
              </div>
              <div class="flex-1 min-w-0">
                <p class="text-sm font-medium truncate">{{ player.player }}</p>
                <p class="text-xs text-muted-foreground">{{ player.isRoomHost ? '房主' : '玩家' }}</p>
              </div>
            </li>
          </ul>
        </div>

        <WatermarkShare
          v-if="pcStore.hostRoomCodePc"
          :room-code="pcStore.hostRoomCodePc"
        />

        <!-- Stop button -->
        <Button variant="destructive" class="w-full" @click="showStopDialog = true">
          <StopCircleOutline class="size-4 mr-2" />
          关闭房间
        </Button>
      </div>
    </template>

    <!-- Stop confirmation dialog -->
    <Dialog :open="showStopDialog" @update:open="showStopDialog = $event">
      <DialogContent class="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{{ pcStore.pcHostError || '确认关闭房间？' }}</DialogTitle>
        </DialogHeader>
        <p v-if="!pcStore.pcHostError" class="text-sm text-muted-foreground">关闭后所有玩家将被断开连接</p>
        <DialogFooter>
          <Button v-if="!pcStore.pcHostError" variant="outline" @click="showStopDialog = false">取消</Button>
          <Button variant="destructive" @click="handleStop">返回首页</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
