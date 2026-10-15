// Loaded on demand by ./feedback.ts: TDesign's message and dialog plugins
// (and their runtime) stay out of the first-screen bundle. Deep imports keep
// the rest of the library out of this chunk.
import 'tdesign-vue-next/es/message/style/css.mjs'
import 'tdesign-vue-next/es/dialog/style/css.mjs'
export { MessagePlugin } from 'tdesign-vue-next/es/message/plugin'
export { DialogPlugin } from 'tdesign-vue-next/es/dialog/plugin'
