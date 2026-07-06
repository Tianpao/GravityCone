<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Separator } from '@/components/ui/separator'
import { ReloadOutline } from '@vicons/ionicons5'
import { useLanStore } from '@/stores/lan'
import { useScaffoldingStore } from '@/stores/scaffolding'
import { useUserStore } from '@/stores/user'
import { useRouter } from 'vue-router'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const lan = useLanStore()
const scaffold = useScaffoldingStore()
const user = useUserStore()
const router = useRouter()
const manualPort = ref('')
const selectedIndex = ref(-1)
let pollTimer: ReturnType<typeof setInterval> | null = null

const isValidPort = computed(() => {
  const p = parseInt(manualPort.value)
  return !isNaN(p) && p > 1024 && p <= 65535
})

function selectServer(index: number) {
  selectedIndex.value = index
  const server = lan.servers[index]
  if (server) {
    manualPort.value = String(server.port)
  }
}

async function handleCreate() {
  const port = parseInt(manualPort.value)
  if (isNaN(port)) return

  const ip = selectedIndex.value >= 0
    ? lan.servers[selectedIndex.value]?.ip ?? '127.0.0.1'
    : '127.0.0.1'

  const playerName = user.user?.username || 'Player'

  try {
    await scaffold.createRoom(port, playerName)
    emit('update:open', false)
    router.push('/host-room')
  } catch {
    // error displayed in store
  }
}

function startPolling() {
  lan.startDiscovery()
  pollTimer = setInterval(() => lan.refresh(), 2000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
  lan.stopDiscovery()
  selectedIndex.value = -1
  manualPort.value = ''
}

watch(() => props.open, (val) => {
  if (val) {
    startPolling()
  } else {
    stopPolling()
  }
})

onUnmounted(() => {
  stopPolling()
})
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>创建房间</DialogTitle>
        <DialogDescription>发现局域网中的 Minecraft 游戏，或手动输入端口</DialogDescription>
      </DialogHeader>

      <!-- LAN Discovery List -->
      <div class="space-y-2">
        <div class="flex items-center justify-between">
          <span class="text-sm font-medium">局域网游戏</span>
          <Button variant="ghost" size="xs" @click="lan.refresh()" :disabled="lan.discovering">
            <ReloadOutline class="size-3.5" :class="{ 'animate-spin': lan.discovering }" />
          </Button>
        </div>

        <!-- Loading -->
        <div
          v-if="lan.discovering && lan.servers.length === 0"
          class="flex items-center justify-center py-6 text-sm text-muted-foreground"
        >
          正在扫描局域网...
        </div>

        <!-- Empty -->
        <div
          v-else-if="lan.servers.length === 0"
          class="flex items-center justify-center py-6 text-sm text-muted-foreground"
        >
          未发现局域网游戏
        </div>

        <!-- Server List -->
        <ul v-else class="space-y-1 max-h-48 overflow-y-auto">
          <li
            v-for="(server, i) in lan.servers"
            :key="`${server.ip}:${server.port}`"
            @click="selectServer(i)"
            class="cursor-pointer rounded-lg px-3 py-2 transition-colors hover:bg-muted"
            :class="{ 'bg-muted ring-2 ring-primary/50': selectedIndex === i }"
          >
            <div class="text-sm font-medium">{{ server.motd || '未知服务器' }}</div>
            <div class="text-xs text-muted-foreground">{{ server.ip }}:{{ server.port }}</div>
          </li>
        </ul>
      </div>

      <!-- Error -->
      <div v-if="lan.error || scaffold.error" class="rounded-lg bg-destructive/10 px-3 py-2 text-xs text-destructive">
        {{ scaffold.error || lan.error }}
      </div>

      <!-- Separator -->
      <div class="flex items-center gap-2">
        <Separator class="flex-1" />
        <span class="text-xs text-muted-foreground">或</span>
        <Separator class="flex-1" />
      </div>

      <!-- Manual Port Input -->
      <div class="space-y-1.5">
        <label class="text-sm font-medium">端口号</label>
        <Input
          v-model="manualPort"
          placeholder="25565"
          type="number"
          min="1"
          max="65535"
        />
      </div>

      <DialogFooter>
        <Button variant="outline" @click="emit('update:open', false)" :disabled="scaffold.creating">取消</Button>
        <Button @click="handleCreate" :disabled="!isValidPort || scaffold.creating">
          <template v-if="scaffold.creating">创建中...</template>
          <template v-else>创建</template>
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
