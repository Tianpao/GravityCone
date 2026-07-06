<script setup lang="ts">
import { ref, computed, watch, nextTick } from 'vue'
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
import { useScaffoldingStore } from '@/stores/scaffolding'
import { useUserStore } from '@/stores/user'
import { useRouter } from 'vue-router'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const scaffold = useScaffoldingStore()
const user = useUserStore()
const router = useRouter()
const rawInput = ref('')
const inputRef = ref<HTMLInputElement | null>(null)

// Allowed charset: 0-9, A-H, J-N, P-Z (no I, O)
const DATA_CHARS = '0123456789ABCDEFGHJKLMNPQRSTUVWXYZ'

function extractDataChars(val: string): string {
  let s = val.toUpperCase()
  if (s.startsWith('U/') || s.startsWith('U：')) {
    s = s.slice(2)
  }
  return s.replace(/[^0-9A-HJ-NP-Z]/g, '').slice(0, 16)
}

function formatDisplay(data: string): string {
  let result = ''
  if (data.length > 0) result += data.slice(0, 4)
  if (data.length > 4) result += '-' + data.slice(4, 8)
  if (data.length > 8) result += '-' + data.slice(8, 12)
  if (data.length > 12) result += '-' + data.slice(12, 16)
  return result
}

const displayValue = computed(() => formatDisplay(rawInput.value))
const isComplete = computed(() => rawInput.value.length === 16)

function handleInput(e: Event) {
  const target = e.target as HTMLInputElement
  const data = extractDataChars(target.value)
  rawInput.value = data
  nextTick(() => {
    if (inputRef.value) {
      const formatted = formatDisplay(data)
      inputRef.value.setSelectionRange(formatted.length, formatted.length)
    }
  })
}

function handlePaste(e: ClipboardEvent) {
  e.preventDefault()
  const pasted = e.clipboardData?.getData('text') || ''
  const existing = rawInput.value
  const pastedData = extractDataChars(pasted)
  const combined = existing + pastedData
  rawInput.value = combined.slice(0, 16)
  nextTick(() => {
    if (inputRef.value) {
      const formatted = formatDisplay(rawInput.value)
      inputRef.value.setSelectionRange(formatted.length, formatted.length)
    }
  })
}

async function handleJoin() {
  if (!isComplete.value) return
  const code = 'U/' + formatDisplay(rawInput.value)
  const playerName = user.user?.username || 'Player'
  try {
    await scaffold.joinRoom(code, playerName)
    emit('update:open', false)
    rawInput.value = ''
    router.push('/joined-room')
  } catch {
    // error displayed in store
  }
}

function handleCancelJoin() {
  scaffold.cancelJoin()
}

watch(() => props.open, (val) => {
  if (!val) {
    rawInput.value = ''
    scaffold.guestError = ''
  }
})
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>加入房间</DialogTitle>
        <DialogDescription>输入房主分享的房间代码</DialogDescription>
      </DialogHeader>

      <div class="space-y-3">
        <div class="relative">
          <span class="absolute left-3 top-1/2 -translate-y-1/2 font-mono text-lg text-muted-foreground pointer-events-none select-none">U/</span>
          <Input
            ref="inputRef"
            :model-value="displayValue"
            @input="handleInput"
            @paste="handlePaste"
            placeholder="ABCD-1234-EFGH-5678"
            class="font-mono text-center text-lg tracking-wider pl-9"
            :disabled="scaffold.joining"
          />
        </div>
        <p v-if="!isComplete && rawInput.length > 0 && !scaffold.joining" class="text-xs text-muted-foreground text-center">
          还需输入 {{ 16 - rawInput.length }} 个字符
        </p>
        <!-- Joining progress -->
        <div v-if="scaffold.joining" class="flex items-center justify-center gap-2 text-sm text-muted-foreground">
          <div class="size-4 animate-spin rounded-full border-2 border-primary border-t-transparent" />
          <span>正在连接房间...</span>
        </div>
      </div>

      <!-- Error -->
      <div v-if="scaffold.guestError" class="rounded-lg bg-destructive/10 px-3 py-2 text-xs text-destructive">
        {{ scaffold.guestError }}
      </div>

      <DialogFooter>
        <template v-if="scaffold.joining">
          <Button variant="outline" @click="handleCancelJoin">
            取消加入
          </Button>
        </template>
        <template v-else>
          <Button variant="outline" @click="emit('update:open', false)">取消</Button>
          <Button @click="handleJoin" :disabled="!isComplete">
            加入
          </Button>
        </template>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
