<script setup lang="ts">
import { onMounted, watch, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useScaffoldingStore } from '@/stores/scaffolding'
import { useWatermarkStore } from '@/stores/watermark'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { useClipboard } from '@vueuse/core'
import { CopyOutline, StopCircleOutline, CheckmarkOutline } from '@vicons/ionicons5'
import WatermarkShare from '@/components/WatermarkShare.vue'
import PlayerList from '@/components/PlayerList.vue'

const router = useRouter()
const scaffold = useScaffoldingStore()
const watermark = useWatermarkStore()
const { copy, copied } = useClipboard()
const showStopDialog = ref(false)
const stopReason = ref('')

onMounted(() => {
  if (scaffold.roomStatus) {
    scaffold.startHostEvents()
  }
  watermark.loadDemoImages()
})

watch(() => scaffold.roomStatus, (val) => {
  if (!val && scaffold.hostError) {
    stopReason.value = scaffold.hostError
    showStopDialog.value = true
  }
})

async function handleStop() {
  await scaffold.stopRoom()
  router.push('/')
}

function handleBackHome() {
  showStopDialog.value = false
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
      <PlayerList :players="scaffold.roomStatus.players ?? []" />

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

    <!-- Room Stopped Dialog -->
    <Dialog :open="showStopDialog" @update:open="showStopDialog = $event">
      <DialogContent class="sm:max-w-sm" @pointer-down-outside.prevent @escape-key-down.prevent>
        <DialogHeader>
          <DialogTitle>房间已关闭</DialogTitle>
          <DialogDescription>{{ stopReason }}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button class="w-full" @click="handleBackHome">返回首页</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
