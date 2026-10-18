# Misskey 猫耳实现与许可核查

核查日期：2026-10-18。上游版本：
[`70ac647faa6e131ed75bc54183c63914fc69d7e6`](https://github.com/misskey-dev/misskey/tree/70ac647faa6e131ed75bc54183c63914fc69d7e6)。

## 核查结果

Misskey 的普通猫耳由头像组件中的 HTML/CSS 绘制，并非单独的 PNG
素材。头像装饰列表是另一个功能，装饰图片需要按各自来源另查许可。

[`MkAvatar.vue`](https://github.com/misskey-dev/misskey/blob/70ac647faa6e131ed75bc54183c63914fc69d7e6/packages/frontend/src/components/global/MkAvatar.vue)
的文件头明确声明：

```text
SPDX-FileCopyrightText: syuilo and misskey-project
SPDX-License-Identifier: AGPL-3.0-only
```

上游根目录的
[`LICENSE`](https://github.com/misskey-dev/misskey/blob/70ac647faa6e131ed75bc54183c63914fc69d7e6/LICENSE)
提供 GNU Affero General Public License v3 正文。

该许可允许使用、修改和商业用途。直接复制或改写该组件时，需要保留
版权和许可声明，依 AGPL-3.0 提供所覆盖作品的对应源代码，并遵守适用的
网络交互源码提供义务。不能把它作为无条件、无署名的公共领域素材处理。

## 本站实现

Community 的猫耳使用独立编写的 SVG 路径和样式，不引入上述 Misskey
组件的代码或素材。界面保留本站主题；账号设置保存为 `is_cat`，联邦资料
按 Misskey 的 JSON-LD 扩展公开为 `isCat`。兼容协议字段不等于复制组件。

本次新增的二维码编码器 `qrcode-generator` 2.0.4 使用 MIT 许可，作者为
Kazuhiko Arase。发行包保留其版权头，完整许可随前端发布在
`/licenses/qrcode-generator.txt`。
