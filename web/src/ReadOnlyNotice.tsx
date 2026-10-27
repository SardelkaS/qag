export function ReadOnlyNotice({
  environment = false,
}: {
  environment?: boolean
}) {
  const title = environment
    ? 'Системное окружение встроенного примера'
    : 'Встроенный пример'
  const text = environment
    ? 'Окружение, переменные и ресурсы нельзя редактировать.'
    : 'Сценарий и его системное окружение нельзя редактировать. Можно запускать вызовы, проходить тест и сохранять результаты.'
  return (
    <section
      className="readonly-notice"
      aria-label={environment
        ? 'Ограничения системного окружения'
        : 'Ограничения встроенного примера'}
    >
      <strong>{title}</strong>
      <p>{text}</p>
    </section>
  )
}
