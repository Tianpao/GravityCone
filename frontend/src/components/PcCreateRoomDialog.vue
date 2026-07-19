<script setup lang="ts">
import { watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePaperConnectStore } from '@/stores/paperconnect'
import { useUserStore } from '@/stores/user'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{ 'update:open': [value: boolean] }>()

const pcStore = usePaperConnectStore()
const user = useUserStore()
const router = useRouter()

async function handleCreate() {
  const playerName = user.user?.username || 'Player'
  try {
    await pcStore.pcCreateRoom(playerName)
    emit('update:open', false)
    router.push('/pc-host-room')
  } catch {
    // Error displayed in store
  }
}

watch(() => props.open, (val) => {
  if (val) pcStore.pcHostError = ''
})
</script>

<template>
  <Dialog :open="props.open" @update:open="emit('update:open', $event)">
    <DialogContent class="sm:max-w-md">
      <DialogHeader>
        <DialogTitle>创建基岩版房间</DialogTitle>
      </DialogHeader>

      <div class="space-y-4 py-2">
        <p class="text-sm text-muted-foreground">创建一个基岩版联机房间，其他玩家可以通过联机码加入</p>

        <div v-if="pcStore.pcHostError" class="bg-destructive/10 text-destructive text-sm p-2 rounded">
          {{ pcStore.pcHostError }}
        </div>
      </div>

      <DialogFooter class="flex gap-2">
        <Button variant="outline" @click="emit('update:open', false)">取消</Button>
        <Button :disabled="pcStore.pcCreating" @click="handleCreate">
          {{ pcStore.pcCreating ? '创建中...' : '创建房间' }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
