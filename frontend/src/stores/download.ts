import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'

type EventUnsubscriber = () => void

export interface DownloadProgress {
  step: 'downloading' | 'extracting'
  percent: number
  totalSize: number
  speed: number
}

export const useDownloadStore = defineStore('download', {
  state: () => ({
    progress: null as DownloadProgress | null,
    downloading: false,
    _unsubscriber: null as EventUnsubscriber | null,
    _timeoutId: null as ReturnType<typeof setTimeout> | null,
  }),

  actions: {
    startListening() {
      if (this._unsubscriber) return

      this._unsubscriber = Events.On('download.progress', (event: any) => {
        const data: DownloadProgress = event.data
        this.progress = data
        this.downloading = true
        if (data.step === 'extracting' && data.percent >= 100) {
          this._timeoutId = setTimeout(() => {
            this.downloading = false
            this.progress = null
            this._timeoutId = null
          }, 500)
        }
      })
    },

    stopListening() {
      if (this._timeoutId) {
        clearTimeout(this._timeoutId)
        this._timeoutId = null
      }
      if (this._unsubscriber) {
        this._unsubscriber()
        this._unsubscriber = null
      }
    },
  },
})
