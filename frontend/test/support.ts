// jsdom lacks pointer-capture, scrollIntoView and ResizeObserver, which the
// library's Reka-based controls need.
import {nextTick} from 'vue'
const proto = Element.prototype as unknown as Record<string, unknown>
proto.hasPointerCapture ??= () => false
proto.setPointerCapture ??= () => undefined
proto.releasePointerCapture ??= () => undefined
proto.scrollIntoView ??= () => undefined
;(globalThis as Record<string, unknown>).ResizeObserver ??= class {observe() {} unobserve() {} disconnect() {}}

export async function settle(count = 20): Promise<void> {for (let i = 0; i < count; i++) {await Promise.resolve(); await nextTick()}}

// Controls are found the way a user does: through their visible <label>.
export function fieldsByLabel(scope: ParentNode, text: string): HTMLElement[] {
  const doc = (scope as Node).ownerDocument ?? (scope as Document)
  return [...scope.querySelectorAll<HTMLLabelElement>('label[for]')]
    .filter(l => l.textContent?.trim() === text)
    .map(l => doc.getElementById(l.htmlFor))
    .filter((el): el is HTMLElement => !!el)
}

export function byLabel(scope: ParentNode, text: string): HTMLElement {
  const [first] = fieldsByLabel(scope, text)
  if (!first) throw new Error(`No control labelled: ${text}`)
  return first
}
