# Шрифты отчёта

Приложение включает Noto Sans Regular и Noto Sans Mono Regular для автономного PDF/Canvas-экспорта с кириллицей. Оригинальная SIL Open Font License приложена в `OFL.txt`.

Официальные источники:

- https://github.com/notofonts/noto-fonts/tree/main/hinted/ttf/NotoSans
- https://github.com/notofonts/noto-fonts/tree/main/hinted/ttf/NotoSansMono
- https://github.com/notofonts/noto-fonts/blob/main/LICENSE

SHA-256 файлов:

```text
b85c38ecea8a7cfb39c24e395a4007474fa5a4fc864f6ee33309eb4948d232d5  NotoSans-Regular.ttf
d9e2b23d19f8230be7146f409a52b1d23117e635e28f2e2892cf91b7382f325b  NotoSansMono-Regular.ttf
```

Рабочие зависимости закреплены на pdf-lib 1.17.1 и @pdf-lib/fontkit 1.1.1. pdfjs-dist 6.4.299 используется только для независимой проверки PDF.
