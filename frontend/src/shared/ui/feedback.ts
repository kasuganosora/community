/**
 * Lazy facades for TDesign's MessagePlugin and DialogPlugin.
 *
 * Both are used from first-screen components (post cards, sidebars) but only
 * after a user action, so the TDesign code behind them is fetched on first
 * use instead of with the app shell. Call sites keep the TDesign API.
 */
type Feedback = typeof import('./tdesignFeedback')
type DialogOptions = Parameters<Feedback['DialogPlugin']['confirm']>[0]
type DialogInstance = ReturnType<Feedback['DialogPlugin']['confirm']>

let loading: Promise<Feedback> | null = null
function load(): Promise<Feedback> {
  loading ??= import('./tdesignFeedback').catch((err) => { loading = null; throw err })
  return loading
}

/** Starts fetching the plugins ahead of use (e.g. when idle). */
export function preloadFeedback(): void { void load().catch(() => {}) }

type MessageArgs = [message: string | Record<string, unknown>, duration?: number]
function message(kind: 'success' | 'error' | 'info' | 'warning') {
  return (...args: MessageArgs): void => {
    load()
      .then(m => { (m.MessagePlugin[kind] as (...a: MessageArgs) => unknown)(...args) })
      .catch(() => { if (kind === 'error') console.error(args[0]) })
  }
}

export const MessagePlugin = {
  success: message('success'),
  error: message('error'),
  info: message('info'),
  warning: message('warning'),
}

type LazyDialog = Pick<DialogInstance, 'destroy' | 'hide' | 'show' | 'setConfirmLoading' | 'update'>

function dialog(kind: 'confirm' | 'alert') {
  return (options: DialogOptions): LazyDialog => {
    let instance: DialogInstance | null = null
    let destroyed = false
    const queued: Array<(d: DialogInstance) => void> = []
    load().then((m) => {
      if (destroyed) return
      instance = m.DialogPlugin[kind](options)
      queued.splice(0).forEach(fn => fn(instance!))
    }).catch(err => console.error('[dialog] failed to load', err))
    const call = <K extends keyof LazyDialog>(name: K) => ((...args: unknown[]) => {
      if (name === 'destroy' && !instance) { destroyed = true; return }
      if (instance) return (instance[name] as (...a: unknown[]) => unknown)(...args)
      queued.push(d => (d[name] as (...a: unknown[]) => unknown)(...args))
    }) as LazyDialog[K]
    return { destroy: call('destroy'), hide: call('hide'), show: call('show'), setConfirmLoading: call('setConfirmLoading'), update: call('update') }
  }
}

export const DialogPlugin = { confirm: dialog('confirm'), alert: dialog('alert') }
