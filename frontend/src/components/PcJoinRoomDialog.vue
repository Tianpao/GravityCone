<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePaperConnectStore } from '@/stores/paperconnect'
import { useUserStore } from '@/stores/user'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'

const DATA_CHARS = '0123456789ABCDEFGHJKLMNPQRSTUVWXYZ'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const pcStore = usePaperConnectStore()
const user = useUserStore()
const router = useRouter()
const rawInput = ref('')

function extractDataChars(val: string): string {
  let s = val.toUpperCase()
  s = s.replace(/^P[/]/, '')
  s = s.replace(/[^0123456789ABCDEFGHJKLMNPQRSTUVWXYZ]/g, '')
  return s.slice(0, 16)
}

function formatDisplay(data: string): string {
  let result = ''
  for (let i = 0; i < data.length; i++) {
    if (i > 0 && i % 4 === 0) result += '-'
    result += data[i]
  }
  return result
}

const displayValue = computed(() => {
  const data = extractDataChars(rawInput.value)
  return formatDisplay(data)
})

const isComplete = computed(() => {
  const data = extractDataChars(rawInput.value)
  return data.length === 16
})

async function handleJoin() {
  const data = extractDataChars(rawInput.value)
  const code = 'P/' + formatDisplay(data)
  const playerName = user.user?.username || 'Player'
  try {
    await pcStore.pcJoinRoom(code, playerName)
    emit('update:open', false)
    router.push('/pc-joined-room')
  } catch {
    // Error displayed in store
  }
}

function handleInput(e: Event) {
  const target = e.target as HTMLInputElement
  rawInput.value = target.value
}

function handlePaste(e: ClipboardEvent) {
  e.preventDefault()
  const text = e.clipboardData?.getData('text') || ''
  rawInput.value = text
}

watch(() => props.open, (val) => {
  if (val) {
    rawInput.value = ''
    pcStore.pcGuestError = ''
  }
})
</script>

<template>
  <Dialog :open="props.open" @update:open="emit('update:open', $event)">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>加入基岩版房间</DialogTitle>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground font-mono text-sm">P/</span>
          <Input
            :model-value="displayValue"
            @input="handleInput"
            @paste="handlePaste"
            placeholder="ABCD-1234-EFGH-5678"
            class="font-mono text-center text-lg tracking-wider pl-9"
          />
        </div>

        <div v-if="pcStore.pcGuestError" class="bg-destructive/10 text-destructive text-sm p-2 rounded">
          {{ pcStore.pcGuestError }}
        </div>

        <div v-if="pcStore.pcJoining" class="flex items-center justify-center gap-2 text-muted-foreground">
          <div class="size-4 animate-spin rounded-full border-2 border-primary border-t-transparent"></div>
          <span class="text-sm">正在连接房间...</span>
        </div>
      </div>

      <DialogFooter class="flex gap-2">
        <template v-if="pcStore.pcJoining">
          <Button variant="outline" @click="pcStore.pcCancelJoin()">取消加入</Button>
        </template>
        <template v-else>
          <Button variant="outline" @click="emit('update:open', false)">取消</Button>
          <Button :disabled="!isComplete" @click="handleJoin">加入房间</Button>
        </template>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
