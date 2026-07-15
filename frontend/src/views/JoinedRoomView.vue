<script setup lang="ts">
import { onMounted, watch, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useScaffoldingStore } from '@/stores/scaffolding'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useClipboard } from '@vueuse/core'
import { CopyOutline, LogOutOutline, CheckmarkOutline } from '@vicons/ionicons5'

const router = useRouter()
const scaffold = useScaffoldingStore()
const { copy, copied } = useClipboard()
const showDisconnectDialog = ref(false)
const disconnectReason = ref('')

onMounted(() => {
  if (scaffold.connectionStatus) {
    scaffold.startGuestEvents()
  }
})

watch(() => scaffold.connectionStatus?.connected, (connected) => {
  if (connected === false && scaffold.connectionStatus?.disconnect_reason) {
    disconnectReason.value = scaffold.connectionStatus.disconnect_reason
    showDisconnectDialog.value = true
  }
})

function handleBackHome() {
  showDisconnectDialog.value = false
  scaffold.leaveRoom()
  router.push('/')
}

async function handleLeave() {
  await scaffold.leaveRoom()
  router.push('/')
}

function copyAddress() {
  if (scaffold.connectionStatus?.mc_address && scaffold.connectionStatus?.mc_port) {
    copy(`${scaffold.connectionStatus.mc_address}:${scaffold.connectionStatus.mc_port}`)
  }
}
</script>

<template>
  <div class="flex flex-1 flex-col items-center justify-center gap-6 px-6">
    <div v-if="scaffold.connectionStatus" class="w-full max-w-sm space-y-6">
      <!-- Connection Status -->
      <div class="flex items-center justify-center gap-2">
        <span class="size-2.5 rounded-full" :class="scaffold.connectionStatus.connected ? 'bg-green-500' : 'bg-red-500'" />
        <span class="text-sm" :class="scaffold.connectionStatus.connected ? 'text-green-500' : 'text-red-500'">
          {{ scaffold.connectionStatus.connected ? '已连接' : '连接断开' }}
        </span>
        <span v-if="scaffold.connectionStatus.heartbeating" class="text-xs text-muted-foreground">· 心跳正常</span>
      </div>

      <!-- Disconnect reason banner -->
      <div v-if="!scaffold.connectionStatus.connected && scaffold.connectionStatus.disconnect_reason" class="rounded-xl border border-destructive/30 bg-destructive/5 p-4 text-center">
        <p class="text-sm text-destructive font-medium">{{ scaffold.connectionStatus.disconnect_reason }}</p>
        <p class="text-xs text-muted-foreground mt-1">正在返回首页...</p>
      </div>

      <!-- Room Code -->
      <div class="rounded-xl border border-border bg-card p-4 text-center space-y-2">
        <p class="text-xs text-muted-foreground">房间代码</p>
        <p class="font-mono text-lg font-bold tracking-widest break-all">
          {{ scaffold.connectionStatus.room_code }}
        </p>
      </div>

      <!-- Server Address -->
      <div v-if="scaffold.connectionStatus.mc_port" class="rounded-xl border border-border bg-card p-4 text-center space-y-2">
        <p class="text-xs text-muted-foreground">游戏地址</p>
        <p class="font-mono text-sm">
          {{ scaffold.connectionStatus.mc_address }}:{{ scaffold.connectionStatus.mc_port }}
        </p>
        <Button variant="ghost" size="xs" @click="copyAddress" class="gap-1">
          <component :is="copied ? CheckmarkOutline : CopyOutline" class="size-3" />
          <span class="text-xs">{{ copied ? '已复制' : '复制地址' }}</span>
        </Button>
      </div>

      <!-- Server Not Started -->
      <div v-else class="rounded-xl border border-yellow-500/30 bg-yellow-500/5 p-4 text-center">
        <p class="text-sm text-yellow-500">Minecraft 服务器尚未启动</p>
        <p class="text-xs text-muted-foreground mt-1">等待房主启动服务器...</p>
      </div>

      <!-- Player List -->
      <div class="rounded-xl border border-border bg-card p-4 space-y-3">
        <div class="flex items-center justify-between">
          <p class="text-xs text-muted-foreground uppercase tracking-wider">玩家列表</p>
          <span class="text-xs text-muted-foreground">{{ scaffold.connectionStatus.online_count ?? 0 }} 人</span>
        </div>

        <div v-if="!scaffold.connectionStatus.players || scaffold.connectionStatus.players.length === 0" class="py-4 text-center text-sm text-muted-foreground">
          加载中...
        </div>

        <ul v-else class="space-y-2">
          <li
            v-for="player in scaffold.connectionStatus.players"
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

      <!-- Leave Button -->
      <Button variant="destructive" class="w-full gap-2" @click="handleLeave">
        <LogOutOutline class="size-4" />
        离开房间
      </Button>
    </div>

    <!-- Loading State -->
    <div v-else class="flex flex-col items-center gap-3">
      <div class="size-8 animate-spin rounded-full border-2 border-primary border-t-transparent" />
      <p class="text-sm text-muted-foreground">正在连接房间...</p>
    </div>

    <!-- Disconnect Dialog -->
    <Dialog :open="showDisconnectDialog" @update:open="showDisconnectDialog = $event">
      <DialogContent class="sm:max-w-sm" @pointer-down-outside.prevent @escape-key-down.prevent>
        <DialogHeader>
          <DialogTitle>连接已断开</DialogTitle>
          <DialogDescription>{{ disconnectReason }}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button class="w-full" @click="handleBackHome">返回首页</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
