declare module 'plain-draggable' {
  interface DraggableOptions {
    autoScroll?: boolean
    snap?: {
      targets: Array<{ x: number; y: number }>
      gravity: number
    } | false
  }

  interface Position {
    left: number
    top: number
  }

  class PlainDraggable {
    constructor(element: HTMLElement, options?: DraggableOptions)
    left: number
    top: number
    snap: DraggableOptions['snap']
    onDragEnd: ((position: Position) => void) | null
    remove(): void
  }

  export = PlainDraggable
}
