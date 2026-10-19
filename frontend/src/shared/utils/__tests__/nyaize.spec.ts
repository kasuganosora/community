import { describe, expect, it } from 'vitest'
import { nyaizeHtml, nyaizeProse, nyaizeText, speaksAsCat } from '../nyaize'

describe('nyaize', () => {
  it('applies the Japanese, English and Korean rules', () => {
    expect(nyaizeProse('なにナﾅ')).toBe('にゃにニャﾆｬ')
    expect(nyaizeProse('banana NAME')).toBe('banyanya NYAME')
    expect(nyaizeProse('Good morning everyone')).toBe('Good mornyan everynyan')
    expect(nyaizeProse('나는 고양이다. 정말이야? 좋다')).toBe('냐는 고양이다냥. 정말이냥? 좋다냥')
    expect(nyaizeProse('hello')).toBe('hello')
  })

  it('keeps code, URLs, mentions, hashtags, emoji and MFM syntax', () => {
    const text = 'na `na` https://na.example/na @nana@na.example #nana :nana: $[x2 na] <plain>na</plain>\n```\nna\n```'
    expect(nyaizeText(text)).toBe('nya `na` https://na.example/na @nana@na.example #nana :nana: $[x2 nya] <plain>na</plain>\n```\nna\n```')
  })

  it('only rewrites text nodes outside links and code', () => {
    const html = '<p>banana <a href="https://x/@nana" class="u-url mention">@<span>nana</span></a> <a href="/tags/na" class="mention hashtag">#<span>na</span></a> <code>na</code></p><pre>na</pre>'
    expect(nyaizeHtml(html)).toBe('<p>banyanya <a href="https://x/@nana" class="u-url mention">@<span>nana</span></a> <a href="/tags/na" class="mention hashtag">#<span>na</span></a> <code>na</code></p><pre>na</pre>')
  })

  it('needs both is_cat and speak_as_cat', () => {
    expect(speaksAsCat({ is_cat: true })).toBe(true)
    expect(speaksAsCat({ is_cat: true, speak_as_cat: false })).toBe(false)
    expect(speaksAsCat({ is_cat: false, speak_as_cat: true })).toBe(false)
    expect(speaksAsCat(null)).toBe(false)
  })
})
