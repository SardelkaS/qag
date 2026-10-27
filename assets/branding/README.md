# Иконка приложения qag

Исходное изображение сгенерировано 26.10.2026 встроенным инструментом `imagegen`. Генерация выполняется отдельно от сборки и работы приложения.

- `qag-master.png`: исходное изображение с прозрачностью.
- `qag.ico`: PNG-кадры 16, 24, 32, 48, 64, 128 и 256 px.
- `../../web/public/qag-32.png`, `qag-256.png`, `favicon.ico`: производные файлы для Web UI.

Размеры воспроизводятся командой `go run ./cmd/icon-assets`. Windows-сборка встраивает сохранённый ICO через `github.com/akavel/rsrc@v0.10.2`; `.syso` создаётся локально. Иконка проверена в 16/32 px на обеих темах и как RT_ICON/RT_GROUP_ICON внутри PE.

Использованный промпт:

```text
Use case: logo-brand. Asset type: original square app icon for qag, a local manual testing workbench. Primary request: a bold, simple checklist mark that also suggests the letter Q, one clear check mark inside a rounded square outline with a small Q tail. Flat crisp raster design, minimal geometric silhouette, generous padding, strong legibility at 16 and 32 pixels. Palette matches the existing app: deep forest green #20372e, sage green #abc78b and warm ivory #f7f8f5. Transparent background outside the rounded square icon. No lettering, no tiny details, no gradients, no shadow, no mockup, no watermark. One centered icon only. Save the generated master PNG in C:/Users/79188/IdeaProjects/qag/assets/branding/qag-master.png if local file output is supported.
```
