import { describe, expect, it } from 'vitest'
import { project, rubberband, sheetDragIntent, shouldDismissSheet, velocityFrom } from '../spring'

describe('project', () => {
  it('projects a flick with exponential decay', () => {
    // (v/1000) * d / (1-d), d = 0.99 → v * 0.099
    expect(project(1000)).toBeCloseTo(99)
    expect(project(0)).toBe(0)
    expect(project(-500)).toBeCloseTo(-49.5)
  })
})

describe('rubberband', () => {
  it('resists more the further past the edge', () => {
    const near = rubberband(20, 400)
    const far = rubberband(200, 400)
    expect(near).toBeGreaterThan(0)
    expect(far).toBeGreaterThan(near)
    expect(far).toBeLessThan(200)
  })

  it('is an odd function', () => {
    expect(rubberband(-40, 300)).toBeCloseTo(-rubberband(40, 300))
  })
})

describe('shouldDismissSheet', () => {
  const height = 600

  it('stays open when the finger rests above the midpoint', () => {
    expect(shouldDismissSheet(100, 0, height)).toBe(false)
  })

  it('dismisses when the finger rests past the midpoint', () => {
    expect(shouldDismissSheet(400, 0, height)).toBe(true)
  })

  it('dismisses a flick aimed down even from the top', () => {
    // 1200px/s projects ~599px with deceleration 0.998, onto a 600px sheet.
    expect(shouldDismissSheet(0, 1200, height)).toBe(true)
  })

  it('returns a flick aimed up even past the midpoint', () => {
    expect(shouldDismissSheet(500, -2000, height)).toBe(false)
  })

  it('breaks a dead tie by the velocity sign and rests open', () => {
    expect(shouldDismissSheet(height / 2, 0, height)).toBe(false)
    // Shift the release point so the projection lands exactly on the midpoint.
    const down = 40
    expect(shouldDismissSheet(height / 2 - project(down, 0.998), down, height)).toBe(true)
    const up = -40
    expect(shouldDismissSheet(height / 2 - project(up, 0.998), up, height)).toBe(false)
  })
})

describe('velocityFrom', () => {
  it('returns px/s over the recent window', () => {
    const history: Array<[number, number]> = [
      [0, 0],
      [50, 10],
      [100, 30],
    ]
    expect(velocityFrom(history)).toBeCloseTo(300)
  })

  it('is zero without a span', () => {
    expect(velocityFrom([[0, 10]])).toBe(0)
    expect(velocityFrom([])).toBe(0)
  })
})

describe('sheetDragIntent', () => {
  it('takes the sheet when pulling down at the top', () => {
    expect(sheetDragIntent(12, 0, 400)).toBe('sheet')
  })

  it('lets content scroll when pulling up away from the bottom', () => {
    expect(sheetDragIntent(-12, 0, 400)).toBe('content')
  })

  it('takes the sheet when pulling up at the bottom', () => {
    expect(sheetDragIntent(-8, 400, 400)).toBe('sheet')
  })

  it('lets content scroll when pulling down away from the top', () => {
    expect(sheetDragIntent(16, 40, 400)).toBe('content')
  })

  it('treats a short sheet as the sheet in both directions', () => {
    expect(sheetDragIntent(10, 0, 0)).toBe('sheet')
    expect(sheetDragIntent(-10, 0, 0)).toBe('sheet')
  })
})
