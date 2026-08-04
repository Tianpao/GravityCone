<script setup lang="ts">
import { ref } from 'vue'
import { useSettingsStore } from '@/stores/settings'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { AddCircleOutline, TrashOutline } from '@vicons/ionicons5'

const settings = useSettingsStore()
const newPeer = ref('')
const inputError = ref('')

function isValidURL(s: string): boolean {
  try {
    const url = new URL(s)
    return ['http:', 'https:', 'ws:', 'wss:', 'tcp:', 'udp:', 'quic:', 'faketcp:', 'txt:'].includes(url.protocol)
  } catch {
    return false
  }
}

function handleAdd() {
  const trimmed = newPeer.value.trim()
  if (!trimmed) return
  if (!isValidURL(trimmed)) {
    inputError.value = '请输入有效的 URL 地址'
    return
  }
  inputError.value = ''
  settings.addPeer(trimmed)
  newPeer.value = ''
}
</script>

<template>
  <div class="flex flex-1 flex-col gap-4 px-5 py-6">
    <h1 class="text-lg font-semibold">设置</h1>

    <div class="flex flex-col gap-3">
      <div class="text-sm font-medium">节点地址</div>

      <div v-if="settings.customPeers.length > 0" class="flex flex-col gap-2">
        <div
          v-for="peer in settings.customPeers"
          :key="peer"
          class="flex items-center gap-2 rounded-lg border border-border px-3 py-2"
        >
          <span class="min-w-0 flex-1 truncate text-xs">{{ peer }}</span>
          <Button size="icon" variant="ghost" class="size-6 shrink-0" @click="settings.removePeer(peer)">
            <TrashOutline class="size-3.5 text-muted-foreground" />
          </Button>
        </div>
      </div>

      <div class="flex flex-col gap-1">
        <div class="flex items-center gap-2">
          <Input
            v-model="newPeer"
            placeholder="输入节点地址"
            class="text-xs"
            :class="inputError && 'border-destructive'"
            @keydown.enter="handleAdd"
            @input="inputError = ''"
          />
          <Button size="icon" variant="outline" class="size-8 shrink-0" :disabled="!newPeer.trim()" @click="handleAdd">
            <AddCircleOutline class="size-4" />
          </Button>
        </div>
        <span v-if="inputError" class="text-xs text-destructive">{{ inputError }}</span>
      </div>
    </div>

    <Separator />

    <div class="flex flex-col gap-3">
      <div class="text-sm font-medium">联机设置</div>

      <Button
        variant="outline"
        class="w-full justify-between"
        :class="settings.p2pDisabled && 'bg-destructive/10 border-destructive/40 text-destructive hover:bg-destructive/15 hover:text-destructive'"
        @click="settings.toggleP2PDisabled()"
      >
        <span>禁止 P2P（强制走中继节点）</span>
        <span>{{ settings.p2pDisabled ? '开' : '关' }}</span>
      </Button>
      <p v-if="settings.p2pDisabled" class="text-xs text-destructive/80">
        开启后将禁止 P2P 直连，联机流量全部经过中继节点；房主需要能拉取到公共中继节点
      </p>
      <p v-else class="text-xs text-muted-foreground">
        P2P 优先，NAT 打洞失败时自动走中继节点
      </p>
    </div>
  </div>
</template>
