import { describe, it, expect } from 'vitest'
import { isInteractiveClick } from '../dom'

function clickOn(target: Element, container: Element): MouseEvent {
  const e = new MouseEvent('click', { bubbles: true })
  Object.defineProperty(e, 'target', { value: target })
  Object.defineProperty(e, 'currentTarget', { value: container })
  return e
}

describe('isInteractiveClick', () => {
  const card = document.createElement('article')
  card.innerHTML = '<p class="text">hello <a href="#">link</a></p><button>like</button>'
  document.body.appendChild(card)

  it('ignores clicks on nested links and buttons', () => {
    expect(isInteractiveClick(clickOn(card.querySelector('a')!, card))).toBe(true)
    expect(isInteractiveClick(clickOn(card.querySelector('button')!, card))).toBe(true)
  })

  it('treats plain text clicks as card clicks', () => {
    expect(isInteractiveClick(clickOn(card.querySelector('.text')!, card))).toBe(false)
  })

  it('ignores clicks from teleported overlays', () => {
    const overlay = document.createElement('div')
    document.body.appendChild(overlay)
    expect(isInteractiveClick(clickOn(overlay, card))).toBe(true)
  })
})
