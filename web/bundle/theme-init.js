;(function () {
  var preference = 'system'
  try {
    var saved = localStorage.getItem('qag.theme')
    if (['light', 'dark', 'system'].includes(saved)) preference = saved
  } catch (_) {}
  document.documentElement.dataset.themePreference = preference
  var dark =
    preference === 'dark' ||
    (preference === 'system' &&
      window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
})()
