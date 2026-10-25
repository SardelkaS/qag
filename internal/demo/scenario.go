package demo

import "qag/internal/catalog"

const CaseName = "Пример: HTTP, gRPC и PostgreSQL"
const ExpectedItem = `{"id":"example-001","name":"Локальный пример qag","status":"ready","amount":"9007199254740993.125"}`

func Scenario() []catalog.Step {
	httpBlock := func(id string, code int) catalog.Block {
		return catalog.Block{ID: catalog.NewID(), Kind: "http", Resource: "DEMO_HTTP", TimeoutSeconds: 30, HTTP: &catalog.HTTPRequest{Method: "GET", Path: "/items/" + id, BodyMode: "none", Headers: []catalog.RequestHeader{}, Query: []catalog.KeyValue{}}, Expected: catalog.Expectation{StatusCode: &code, BodyMode: "json", Body: ExpectedItem}}
	}
	grpcBlock := func(id, code string) catalog.Block {
		return catalog.Block{ID: catalog.NewID(), Kind: "grpc", Resource: "DEMO_GRPC", TimeoutSeconds: 30, GRPC: &catalog.GRPCRequest{Service: "qag.demo.v1.DemoService", Method: "GetItem", Body: `{"id":"` + id + `"}`, Metadata: []catalog.RequestHeader{}}, Expected: catalog.Expectation{GRPCCode: code, BodyMode: "json", Body: ExpectedItem}}
	}
	missingHTTP, missingGRPC := httpBlock("missing-item", 404), grpcBlock("missing-item", "NOT_FOUND")
	missingHTTP.Expected.BodyMode = "none"
	missingHTTP.Expected.Body = ""
	missingGRPC.Expected.BodyMode = "none"
	missingGRPC.Expected.Body = ""
	return []catalog.Step{
		{ID: catalog.NewID(), Title: "Прочитайте запись по HTTP", Description: "Запустите HTTP-вызов. Проверьте статус 200, поля записи и точную сумму; затем отметьте шаг вручную.", Blocks: []catalog.Block{httpBlock("example-001", 200)}},
		{ID: catalog.NewID(), Title: "Прочитайте ту же запись по gRPC", Description: "Запустите unary GetItem. Сопоставьте ID, название, статус и сумму с HTTP-ответом.", Blocks: []catalog.Block{grpcBlock("example-001", "OK")}},
		{ID: catalog.NewID(), Title: "Проверьте строку в PostgreSQL", Description: "Выполните параметризованный SELECT. Проверьте типы колонок и точное numeric-значение без округления.", Blocks: []catalog.Block{{ID: catalog.NewID(), Kind: "postgresql", Resource: "DEMO_SQL", TimeoutSeconds: 30, SQL: &catalog.SQLRequest{Mode: "rows", Query: "SELECT id, name, status, amount FROM qag_demo.items WHERE id=$1 ORDER BY id", Parameters: []catalog.TypedValue{{Type: "string", Value: "example-001"}}}, Expected: catalog.Expectation{BodyMode: "rows", Columns: []catalog.Column{{Name: "id", Type: "text"}, {Name: "name", Type: "text"}, {Name: "status", Type: "text"}, {Name: "amount", Type: "numeric"}}, Rows: [][]catalog.TypedValue{{{Type: "string", Value: "example-001"}, {Type: "string", Value: "Локальный пример qag"}, {Type: "string", Value: "ready"}, {Type: "decimal", Value: "9007199254740993.125"}}}}}}},
		{ID: catalog.NewID(), Title: "Проверьте отсутствие записи", Description: "Последовательно выполните оба вызова с missing-item. Ожидаемые 404 и NOT_FOUND должны считаться совпадением.", Blocks: []catalog.Block{missingHTTP, missingGRPC}},
		{ID: catalog.NewID(), Title: "Оцените результат вручную", Description: "Сопоставьте результаты трёх протоколов и подписи сравнения. Отметьте ручные статусы всех шагов, при необходимости сохраните комментарий и завершите прогон.", Blocks: []catalog.Block{}},
	}
}
