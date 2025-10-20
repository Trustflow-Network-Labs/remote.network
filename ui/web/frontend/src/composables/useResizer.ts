/**
 * Resizer composable for creating resizable panes
 * Ported from trustflow-node/frontend/src/mixins/window-resizer.js
 */

export function useResizer() {
  const initResizer = (
    containerClass: string,
    leftPaneClass: string,
    rightPaneClass: string,
    resizerClass: string,
    mousedownCallback?: (e: MouseEvent) => void,
    mousemoveCallback?: (e: MouseEvent) => void,
    mouseupCallback?: () => void
  ) => {
    const resizer = document.querySelector(resizerClass) as HTMLElement
    const leftPane = document.querySelector(leftPaneClass) as HTMLElement
    const rightPane = document.querySelector(rightPaneClass) as HTMLElement
    const container = document.querySelector(containerClass) as HTMLElement

    if (!resizer || !leftPane || !rightPane || !container) {
      console.error('Resizer initialization failed: missing elements')
      return
    }

    let isResizing = false

    resizer.addEventListener('mousedown', (e: MouseEvent) => {
      isResizing = true
      document.body.style.cursor = 'col-resize'

      if (mousedownCallback) {
        mousedownCallback(e)
      }
    })

    document.addEventListener('mousemove', (e: MouseEvent) => {
      if (!isResizing) return

      const containerOffsetLeft = container.offsetLeft
      const newLeftWidth = e.clientX - containerOffsetLeft

      leftPane.style.width = `${newLeftWidth}px`

      if (mousemoveCallback) {
        mousemoveCallback(e)
      }
    })

    document.addEventListener('mouseup', () => {
      if (!isResizing) return

      isResizing = false
      document.body.style.cursor = 'default'

      if (mouseupCallback) {
        mouseupCallback()
      }
    })
  }

  return {
    initResizer
  }
}
