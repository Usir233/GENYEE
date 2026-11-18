export interface VersionedRequestCoordinator<_T = unknown> {
  fetch: (force?: boolean, signal?: AbortSignal) => Promise<void>
  invalidate: () => void
  hasInFlightRequest: () => boolean
}

function composeRequestSignal(controller: AbortController, callerSignal?: AbortSignal): AbortSignal {
  if (!callerSignal) return controller.signal
  if (typeof AbortSignal !== 'undefined' && typeof AbortSignal.any === 'function') {
    return AbortSignal.any([controller.signal, callerSignal])
  }
  if (callerSignal.aborted) controller.abort(callerSignal.reason)
  else callerSignal.addEventListener('abort', () => controller.abort(callerSignal.reason), { once: true })
  return controller.signal
}

/**
 * Coordinates cached reads that also need a reliable "force refresh" mode.
 *
 * A forced read requested while another read is running is queued after the
 * current request. Invalidating the coordinator also prevents an older
 * response from overwriting state changed by a newer write.
 */
export function createVersionedRequestCoordinator<T>(
  request: (signal: AbortSignal) => Promise<T>,
  apply: (value: T) => void,
): VersionedRequestCoordinator<T> {
  let revision = 0
  let startedSequence = 0
  let completedSequence = 0
  let forceTargetSequence = 0
  let inFlight: Promise<void> | null = null
  let forceQueue: Promise<void> | null = null

  const start = (callerSignal?: AbortSignal): Promise<void> => {
    const requestRevision = revision
    const sequence = ++startedSequence
    const controller = new AbortController()
    const signal = composeRequestSignal(controller, callerSignal)
    const current = (async () => {
      const value = await request(signal)
      if (requestRevision === revision && !signal.aborted) {
        apply(value)
      }
    })()

    inFlight = current
    void current.then(
      () => {
        completedSequence = Math.max(completedSequence, sequence)
        if (inFlight === current) inFlight = null
      },
      () => {
        completedSequence = Math.max(completedSequence, sequence)
        if (inFlight === current) inFlight = null
      },
    )
    return current
  }

  const fetch = (force = false, callerSignal?: AbortSignal): Promise<void> => {
    if (!inFlight) return start(callerSignal)
    if (!force) return inFlight

    // A force call always requires at least one request newer than the one
    // that was in flight when force was requested.
    forceTargetSequence = Math.max(forceTargetSequence, startedSequence + 1)
    if (!forceQueue) {
      const queued = (async () => {
        while (completedSequence < forceTargetSequence) {
          if (inFlight) {
            try {
              await inFlight
            } catch {
              // The queued forced request must still run after a failed read.
            }
          } else {
            await start(callerSignal)
          }
        }
      })()
      forceQueue = queued
      void queued.then(
        () => {
          if (forceQueue === queued) forceQueue = null
        },
        () => {
          if (forceQueue === queued) forceQueue = null
        },
      )
    }
    return forceQueue
  }

  return {
    fetch,
    invalidate: () => {
      revision += 1
    },
    hasInFlightRequest: () => inFlight !== null,
  }
}
