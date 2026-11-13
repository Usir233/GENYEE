import { defineStore } from 'pinia'
import { get } from '@/utils/request'

export type BrowserAccountStatus = {
  enabled: boolean
  connected: boolean
  device?: { id: string; label: string; last_seen_at: string }
  extension_available: boolean
}

type BrowserConnectionState = BrowserAccountStatus & {
  loaded: boolean
  subscribers: number
}

type PollState = {
  timer?: ReturnType<typeof setTimeout>
  controller?: AbortController
  visibilityHandler?: () => void
  inFlight?: Promise<void>
  generation: number
  requestSequence: number
}

// Polling belongs to one store instance, including when a new Pinia app is
// mounted while an old one still has an HTTP response in flight.
const polling = new WeakMap<object, PollState>()
function pollState(store: object): PollState {
  let state = polling.get(store)
  if (!state) {
    state = { generation: 0, requestSequence: 0 }
    polling.set(store, state)
  }
  return state
}

export const useBrowserConnectionStore = defineStore('browserConnection', {
  state: (): BrowserConnectionState => ({
    enabled: false,
    connected: false,
    extension_available: false,
    loaded: false,
    subscribers: 0,
  }),
  getters: {
    online: (state) => state.enabled && state.connected,
    knownOffline: (state) => state.loaded && !(state.enabled && state.connected),
  },
  actions: {
    apply(data: BrowserAccountStatus) {
      pollState(this).requestSequence++
      this.enabled = data.enabled === true
      this.connected = data.connected === true
      this.device = data.device
      this.extension_available = data.extension_available === true
      this.loaded = true
    },
    async refresh() {
      const state = pollState(this)
      const generation = state.generation
      const sequence = ++state.requestSequence
      const result = await get<{ data: BrowserAccountStatus }>('/api/v1/me/browser', {
        signal: state.controller?.signal,
      })
      if (generation === state.generation && sequence === state.requestSequence) this.apply(result.data)
    },
    watchStatus() {
      this.subscribers += 1
      if (this.subscribers !== 1) return
      const state = pollState(this)
      state.generation++
      state.controller = new AbortController()
      state.visibilityHandler = () => {
        if (!document.hidden) void this.poll()
      }
      document.addEventListener('visibilitychange', state.visibilityHandler)
      void this.poll()
    },
    unwatchStatus() {
      this.subscribers = Math.max(0, this.subscribers - 1)
      if (this.subscribers !== 0) return
      const state = pollState(this)
      state.generation++
      state.inFlight = undefined
      if (state.visibilityHandler) {
        document.removeEventListener('visibilitychange', state.visibilityHandler)
        state.visibilityHandler = undefined
      }
      state.controller?.abort()
      state.controller = undefined
      clearTimeout(state.timer)
      state.timer = undefined
    },
    async poll() {
      if (this.subscribers === 0) return
      const state = pollState(this)
      if (state.inFlight) return state.inFlight
      clearTimeout(state.timer)
      state.timer = undefined
      const generation = state.generation
      const current = (async () => {
        try {
          if (!document.hidden) await this.refresh()
        } catch {
          // Keep the last known status; the composer stays conservative once loaded.
        }
        if (generation === state.generation && this.subscribers > 0) {
          state.timer = setTimeout(() => { void this.poll() }, this.online ? 5000 : 3000)
        }
      })()
      state.inFlight = current
      try {
        await current
      } finally {
        if (state.inFlight === current) state.inFlight = undefined
      }
    },
  },
})
