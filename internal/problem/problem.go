package problem

import (
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Error struct {
	Code    string
	Message string
	Fields  map[string]string
}

func (e *Error) Error() string { return e.Message }
func Invalid(field, message string) error {
	return &Error{Code: "invalid", Message: message, Fields: map[string]string{field: message}}
}
func NotFound() error               { return &Error{Code: "not_found", Message: "Объект не найден"} }
func Conflict(message string) error { return &Error{Code: "conflict", Message: message} }
func Archived() error {
	return Conflict("Сначала восстановите папку и тест-кейс из архива")
}
func Database(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return NotFound()
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505":
			return Invalid("name", "Такое имя уже занято, включая архив")
		case "23503":
			return Conflict("Связанный объект отсутствует или был изменён")
		case "40001", "40P01":
			return Conflict("Данные изменились; обновите страницу и повторите действие")
		}
	}
	return &Error{Code: "storage_unavailable", Message: "Не удалось записать или прочитать данные PostgreSQL; изменения не подтверждены"}
}
