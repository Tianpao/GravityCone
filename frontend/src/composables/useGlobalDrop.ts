import { ref, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { useWatermarkStore } from '@/stores/watermark'
import { useScaffoldingStore } from '@/stores/scaffolding'
import { useUserStore } from '@/stores/user'

export function useGlobalDrop() {
  const router = useRouter()
  const watermark = useWatermarkStore()
  const scaffold = useScaffoldingStore()
  const user = useUserStore()

  const showDropOverlay = ref(false)
  const dropStatus = ref<'idle' | 'joining' | 'error'>('idle')
  const dropRoomCode = ref('')
  const dropError = ref('')

  let dragCounter = 0
  let cancelFlag = false
  let currentDropId = 0           // increment to invalidate stale drops

  function cancel() {
    cancelFlag = true
    scaffold.cancelJoin()
    showDropOverlay.value = false
    dropStatus.value = 'idle'
    dropRoomCode.value = ''
    dropError.value = ''
    dragCounter = 0
  }

  function handleDragEnter(e: DragEvent) {
    e.preventDefault()
    e.stopPropagation()

    if (!e.dataTransfer) return
    if (e.dataTransfer.types && !e.dataTransfer.types.includes('Files')) return

    dragCounter++
    if (dragCounter === 1) {
      showDropOverlay.value = true
      dropStatus.value = 'idle'
    }
  }

  function handleDragOver(e: DragEvent) {
    e.preventDefault()
    e.stopPropagation()
    if (e.dataTransfer) {
      e.dataTransfer.dropEffect = 'copy'
    }
  }

  function handleDragLeave(e: DragEvent) {
    e.preventDefault()
    e.stopPropagation()

    dragCounter--
    if (dragCounter <= 0) {
      showDropOverlay.value = false
    }
  }

  async function handleDrop(e: DragEvent) {
    e.preventDefault()
    e.stopPropagation()
    dragCounter = 0
    cancelFlag = false
    currentDropId++
    const dropId = currentDropId

    const files = e.dataTransfer?.files
    if (!files || files.length === 0) {
      showDropOverlay.value = false
      return
    }

    const file = files[0]
    if (!file.type.startsWith('image/')) {
      showDropOverlay.value = false
      return
    }

    showDropOverlay.value = true
    dropStatus.value = 'joining'
    dropError.value = ''
    dropRoomCode.value = ''

    try {
      const base64 = await fileToBase64(file)
      if (cancelFlag || dropId !== currentDropId) return

      const code = await watermark.decode(base64)
      if (cancelFlag || dropId !== currentDropId) return

      if (!code) {
        dropError.value = watermark.error || '未识别到房间代码'
        dropStatus.value = 'error'
        return
      }

      dropRoomCode.value = code

      const playerName = user.user?.username || 'Player'
      await scaffold.joinRoom(code, playerName)

      // joinRoom completed — check if user cancelled while waiting
      if (cancelFlag) {
        scaffold.leaveRoom()
        return
      }
      if (dropId !== currentDropId) {
        // Another drop started, clean up this stale join
        scaffold.leaveRoom()
        return
      }

      router.push('/joined-room')
      setTimeout(reset, 500)
    } catch (e: any) {
      if (cancelFlag || dropId !== currentDropId) return
      dropError.value = e?.message || '加入房间失败'
      dropStatus.value = 'error'
    }
  }

  function reset() {
    cancelFlag = false
    showDropOverlay.value = false
    dropStatus.value = 'idle'
    dropRoomCode.value = ''
    dropError.value = ''
    dragCounter = 0
  }

  function fileToBase64(file: File): Promise<string> {
    return new Promise((resolve, reject) => {
      const reader = new FileReader()
      reader.onload = () => {
        const result = reader.result as string
        resolve(result.split(',')[1])
      }
      reader.onerror = () => reject(new Error('读取文件失败'))
      reader.readAsDataURL(file)
    })
  }

  onMounted(() => {
    window.addEventListener('dragenter', handleDragEnter)
    window.addEventListener('dragover', handleDragOver)
    window.addEventListener('dragleave', handleDragLeave)
    window.addEventListener('drop', handleDrop)
  })

  onUnmounted(() => {
    window.removeEventListener('dragenter', handleDragEnter)
    window.removeEventListener('dragover', handleDragOver)
    window.removeEventListener('dragleave', handleDragLeave)
    window.removeEventListener('drop', handleDrop)
  })

  return {
    showDropOverlay,
    dropStatus,
    dropRoomCode,
    dropError,
    cancel,
  }
}
