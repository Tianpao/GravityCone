import { defineStore } from 'pinia'
import { Events } from '@wailsio/runtime'
import { Ensure } from '@/../bindings/gravitycone/core/easytier/easytierdownloadservice'

type EventUnsubscriber = () => void
type DownloadStatus = 'idle' | 'downloading' | 'extracting' | 'error'

export interface DownloadProgress {
  step: 'downloading' | 'extracting'
  percent: number
  total_size: number
  speed: number
}

interface DownloadError {
  error: string
}

export const useDownloadStore = defineStore('download', {
  state: () => ({
    progress: null as DownloadProgress | null,
    status: 'idle' as DownloadStatus,
    errorMessage: '',
    _progressUnsubscriber: null as EventUnsubscriber | null,
    _errorUnsubscriber: null as EventUnsubscriber | null,
    _timeoutId: null as ReturnType<typeof setTimeout> | null,
  }),

  actions: {
    startListening() {
      if (this._progressUnsubscriber) return

      this._progressUnsubscriber = Events.On('download.progress', (event: { data: DownloadProgress }) => {
        const data = event.data
        if (this._timeoutId) {
          clearTimeout(this._timeoutId)
          this._timeoutId = null
        }
        this.progress = data
        this.status = data.step
        this.errorMessage = ''
        if (data.step === 'extracting' && data.percent >= 100) {
          this._timeoutId = setTimeout(() => {
            this.status = 'idle'
            this.progress = null
            this._timeoutId = null
          }, 500)
        }
      })

      this._errorUnsubscriber = Events.On('download.error', (event: { data: DownloadError }) => {
        if (this._timeoutId) {
          clearTimeout(this._timeoutId)
          this._timeoutId = null
        }
        this.progress = null
        this.status = 'error'
        this.errorMessage = event.data.error
      })
    },

    async retry() {
      this.progress = null
      this.status = 'idle'
      this.errorMessage = ''
      try {
        await Ensure()
      } catch {
        // The backend emits download.error with the actionable failure message.
      }
    },

    dismiss() {
      this.progress = null
      this.status = 'idle'
      this.errorMessage = ''
    },

    stopListening() {
      if (this._timeoutId) {
        clearTimeout(this._timeoutId)
        this._timeoutId = null
      }
      if (this._progressUnsubscriber) {
        this._progressUnsubscriber()
        this._progressUnsubscriber = null
      }
      if (this._errorUnsubscriber) {
        this._errorUnsubscriber()
        this._errorUnsubscriber = null
      }
    },
  },
})
