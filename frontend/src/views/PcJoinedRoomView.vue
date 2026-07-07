<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePaperConnectStore } from '@/stores/paperconnect'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { LogOutOutline } from '@vicons/ionicons5'

const pcStore = usePaperConnectStore()
const router = useRouter()
const showDisconnectDialog = ref(false)
let pollTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  pollTimer = setInterval(() => {
    pcStore.pcRefreshConnectionStatus()
  }, 3000)
})

onUnmounted(() => {
  if (pollTimer) clearInterval(pollTimer)
})

watch(() => pcStore.pcConnectionStatus?.connected, (now, prev) => {
  if (prev === true && now === false) {
    showDisconnectDialog.value = true
  }
})

async function handleLeave() {
  await pcStore.pcLeaveRoom()
  router.push('/')
}

const players = () => pcStore.pcConnectionStatus?.players ?? []
</script>

<template>
  <div class="flex flex-col items-center justify-center flex-1 p-6">
    <div class="w-full max-w-sm space-y-6">
      <!-- Connection status -->
      <div class="flex items-center gap-2">
        <div class="size-3 rounded-full" :class="pcStore.isConnectedPc ? 'bg-green-500' : 'bg-red-500'"></div>
        <span class="text-sm">{{ pcStore.isConnectedPc ? '已连接' : '连接断开' }}</span>
        <span v-if="pcStore.pcConnectionStatus?.heartbeating" class="text-xs text-muted-foreground">心跳正常</span>
      </div>

      <!-- Disconnect reason banner -->
      <div v-if="!pcStore.isConnectedPc && pcStore.pcConnectionStatus?.disconnect_reason"
           class="border border-destructive/30 bg-destructive/5 p-4 rounded-xl">
        <p class="text-sm text-destructive">{{ pcStore.pcConnectionStatus.disconnect_reason }}</p>
      </div>

      <!-- Room code -->
      <div class="rounded-xl border border-border bg-card p-4 text-center">
        <p class="text-xs uppercase text-muted-foreground mb-1">房间代码</p>
        <p class="font-mono text-lg font-bold tracking-widest break-all">{{ pcStore.pcConnectionStatus?.room_code }}</p>
      </div>

      <!-- Player list -->
      <div class="rounded-xl border border-border bg-card p-4 space-y-3">
        <div class="flex items-center justify-between">
          <p class="text-sm font-medium">在线玩家</p>
          <span class="text-xs text-muted-foreground">{{ pcStore.pcConnectionStatus?.online_count ?? 0 }} 人</span>
        </div>

        <div v-if="players().length === 0" class="text-sm text-muted-foreground text-center py-2">
          加载中...
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

      <!-- Leave button -->
      <Button variant="destructive" class="w-full" @click="handleLeave">
        <LogOutOutline class="size-4 mr-2" />
        退出房间
      </Button>
    </div>

    <!-- Disconnect dialog -->
    <Dialog :open="showDisconnectDialog" @update:open="showDisconnectDialog = $event">
      <DialogContent class="sm:max-w-sm" @pointer-down-outside.prevent @escape-key-down.prevent>
        <DialogHeader>
          <DialogTitle>连接已断开</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">{{ pcStore.pcConnectionStatus?.disconnect_reason || '房主已关闭房间' }}</p>
        <DialogFooter>
          <Button variant="destructive" @click="handleLeave">返回首页</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
