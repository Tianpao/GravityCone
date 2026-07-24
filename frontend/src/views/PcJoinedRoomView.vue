<script setup lang="ts">
import { ref, onMounted, onUnmounted, watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePaperConnectStore } from '@/stores/paperconnect'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { LogOutOutline } from '@vicons/ionicons5'
import PaperConnectPlayerList from '@/components/PaperConnectPlayerList.vue'

const pcStore = usePaperConnectStore()
const router = useRouter()
const showDisconnectDialog = ref(false)
const confirmingMinecraftEnded = ref(false)
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

async function handleMinecraftEnded() {
  confirmingMinecraftEnded.value = true
  await pcStore.pcConfirmMinecraftEnded()
  confirmingMinecraftEnded.value = false
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

      <!-- NetherNet proxy hint -->
      <div v-if="pcStore.isConnectedPc && !pcStore.pcPortBusyMessage" class="rounded-xl border border-primary/20 bg-primary/5 p-3">
        <p class="text-sm text-muted-foreground">打开 Minecraft 基岩版，在局域网游戏中找到 <strong class="text-foreground">GravityCone Proxy</strong> 并加入</p>
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

        <PaperConnectPlayerList :players="players()" empty-text="加载中..." />
      </div>

      <!-- Leave button -->
      <Button variant="destructive" class="w-full" @click="handleLeave">
        <LogOutOutline class="size-4 mr-2" />
        退出房间
      </Button>
    </div>

    <!-- Minecraft port conflict dialog -->
    <Dialog :open="Boolean(pcStore.pcPortBusyMessage)">
      <DialogContent
        class="sm:max-w-sm"
        :show-close-button="false"
        @pointer-down-outside.prevent
        @escape-key-down.prevent
      >
        <DialogHeader>
          <DialogTitle>请结束 Minecraft</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">
          {{ pcStore.pcPortBusyMessage }} 本地游戏广播需要使用 UDP 端口 7551。
        </p>
        <p v-if="pcStore.pcGuestError" class="text-sm text-destructive">{{ pcStore.pcGuestError }}</p>
        <DialogFooter class="gap-2 sm:justify-between">
          <Button variant="outline" :disabled="confirmingMinecraftEnded" @click="handleLeave">取消</Button>
          <Button :disabled="confirmingMinecraftEnded" @click="handleMinecraftEnded">
            {{ confirmingMinecraftEnded ? '正在确认...' : 'Minecraft 已结束' }}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>

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
