package mcpserver

import (
	"context"
	"github.com/SardelkaS/qag/internal/builtin"
	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/clipboard"
	"github.com/SardelkaS/qag/internal/execution"
	"github.com/SardelkaS/qag/internal/problem"
	"github.com/SardelkaS/qag/internal/reporting"
	"github.com/SardelkaS/qag/internal/resources"
	"github.com/SardelkaS/qag/internal/runs"
)

func page(p Page) (int, error) {
	if p.Limit == 0 {
		return 50, nil
	}
	if p.Limit < 1 || p.Limit > 100 {
		return 0, problem.Invalid("limit", "Размер страницы должен быть от 1 до 100")
	}
	return p.Limit, nil
}
func (s *Server) registerTools() {
	e := s.options.Engine
	addTool(s, "qag_status", "Готовность qag, протоколы, пределы и доступность clipboard без приватной конфигурации.", true, false, false, func(ctx context.Context, _ Empty) (Status, error) {
		if s.options.Ping != nil {
			if err := s.options.Ping(ctx); err != nil {
				return Status{}, &problem.Error{Code: "storage_unavailable", Message: "Приложение не готово"}
			}
		}
		return Status{Status: "ready", Protocols: []string{"2026-07-28", "2025-11-25"}, ClipboardNative: clipboard.Available(), Limits: map[string]int64{"requestBytes": 32 << 20, "inlineBytes": InlineBytes, "chunkBytes": ChunkBytes, "artifactBytes": MaxArtifactBytes, "artifactCount": 32, "artifactTotalBytes": 256 << 20, "artifactTTLSeconds": 600, "renderSeconds": 60, "renderConcurrency": 2}}, nil
	})
	addTool(s, "qag_demo_get", "Идентификаторы неизменяемого встроенного кейса и его системного окружения.", true, false, false, func(ctx context.Context, _ Empty) (builtin.Info, error) { return e.Catalog.DemoInfo(ctx) })
	addTool(s, "qag_folders_list", "Папки каталога или архива с алфавитной сортировкой и cursor-пагинацией.", true, false, false, func(ctx context.Context, in FolderList) (catalog.Page[catalog.Folder], error) {
		limit, err := page(in.Page)
		if err != nil {
			return catalog.Page[catalog.Folder]{}, err
		}
		return e.Catalog.ListFolders(ctx, in.Archived, limit, in.Cursor)
	})
	addTool(s, "qag_folder_create", "Создать папку каталога.", false, false, false, func(ctx context.Context, in NameInput) (catalog.Folder, error) {
		return e.Catalog.CreateFolder(ctx, in.Name)
	})
	addTool(s, "qag_folder_update", "Переименовать, архивировать или восстановить папку; пропущенные поля сохраняются.", false, false, false, func(ctx context.Context, in Update) (catalog.Folder, error) {
		return e.Catalog.PatchFolder(ctx, in.ID, in.Name, in.Archived)
	})
	addTool(s, "qag_test_cases_list", "Кейсы папки; архивные следуют за активными, внутри группы сортировка по имени.", true, false, false, func(ctx context.Context, in CasesList) (catalog.Page[catalog.TestCase], error) {
		limit, err := page(in.Page)
		if err != nil {
			return catalog.Page[catalog.TestCase]{}, err
		}
		return e.Catalog.ListCases(ctx, in.FolderID, limit, in.Cursor)
	})
	addTool(s, "qag_test_case_create", "Создать кейс; пустое имя получает свободное имя автоматически.", false, false, false, func(ctx context.Context, in CaseCreate) (catalog.TestCase, error) {
		return e.Catalog.CreateCase(ctx, in.FolderID, in.Name)
	})
	addTool(s, "qag_test_case_get", "Сценарий, revision, шаги, блоки и замечания готовности без выполнения вызовов.", true, false, false, func(ctx context.Context, in IDInput) (catalog.TestCase, error) { return e.Catalog.GetCase(ctx, in.ID) })
	addTool(s, "qag_test_case_save", "Сохранить полный упорядоченный сценарий с revision. Незавершённые пользовательские поля разрешены; IDs новых шагов/блоков — UUID клиента.", false, false, false, func(ctx context.Context, in CaseSave) (catalog.TestCase, error) {
		return e.Catalog.SaveCase(ctx, in.ID, in.Name, in.Revision, in.Steps)
	})
	addTool(s, "qag_test_case_update", "Переименовать, архивировать или восстановить кейс по правилам UI.", false, false, false, func(ctx context.Context, in Update) (catalog.TestCase, error) {
		return e.Catalog.PatchCase(ctx, in.ID, in.Name, in.Archived)
	})
	addTool(s, "qag_image_upload", "Загрузить PNG/JPEG/WebP как fileName/dataBase64. Ссылка назначается шагу/блоку через case_save.", false, false, false, func(ctx context.Context, in ImageUpload) (catalog.ExpectedImage, error) {
		return e.Catalog.UploadImage(ctx, in.TestCaseID, in.ImageUpload)
	})
	addTool(s, "qag_image_get", "Прочитать эталон своего кейса: метаданные и защищённый артефакт исходных байтов.", true, false, false, func(ctx context.Context, in ImageGet) (ImageResult, error) {
		info, data, err := e.Catalog.ReadImage(ctx, in.TestCaseID, in.ImageID)
		if err != nil {
			return ImageResult{}, err
		}
		files, err := s.Artifacts.PutGroup([]File{{Name: info.Filename, MIMEType: info.ContentType, Data: data}})
		if err != nil {
			return ImageResult{}, err
		}
		return ImageResult{Image: info, Artifact: files[0]}, nil
	})
	addTool(s, "qag_environments_list", "Окружения с пагинацией, открытыми переменными и только именами секретов.", true, false, false, func(ctx context.Context, in Page) (catalog.Page[resources.Environment], error) {
		limit, err := page(in)
		if err != nil {
			return catalog.Page[resources.Environment]{}, err
		}
		return e.Resources.ListEnvironments(ctx, limit, in.Cursor)
	})
	addTool(s, "qag_environment_get", "Публичные настройки одного окружения без значений секретов.", true, false, false, func(ctx context.Context, in IDInput) (resources.Environment, error) {
		return e.Resources.GetEnvironment(ctx, in.ID)
	})
	addTool(s, "qag_environment_create", "Создать окружение только с именем и необязательным описанием.", false, false, false, func(ctx context.Context, in EnvironmentCreate) (resources.Environment, error) {
		return e.Resources.SaveEnvironmentMetadata(ctx, "", in.Name, in.Description, 0)
	})
	addTool(s, "qag_environment_update", "Изменить имя/описание окружения по revision, сохранив переменные и секреты.", false, false, false, func(ctx context.Context, in EnvironmentUpdate) (resources.Environment, error) {
		return e.Resources.SaveEnvironmentMetadata(ctx, in.ID, in.Name, in.Description, in.Revision)
	})
	addTool(s, "qag_variables_set", "Заменить полный набор открытых переменных; secrets.replace/clear изменяют секреты, пропущенные секреты сохраняются.", false, false, false, func(ctx context.Context, in VariablesSet) (resources.Environment, error) {
		return e.Resources.SaveVariables(ctx, in.ID, in.Revision, in.Variables, in.Secrets.domain())
	})
	addTool(s, "qag_resources_list", "Ресурсы выбранного окружения без приватных значений.", true, false, false, func(ctx context.Context, in ResourceList) (catalog.Page[resources.Resource], error) {
		limit, err := page(in.Page)
		if err != nil {
			return catalog.Page[resources.Resource]{}, err
		}
		return e.Resources.ListResources(ctx, in.EnvironmentID, limit, in.Cursor)
	})
	addTool(s, "qag_resource_get", "Публичные адреса, TLS/mTLS, headers/sources и proto bundle ресурса; только имена секретов.", true, false, false, func(ctx context.Context, in IDInput) (resources.Resource, error) {
		return e.Resources.GetResource(ctx, in.ID)
	})
	addTool(s, "qag_resource_create", "Создать HTTP/gRPC/PostgreSQL-ресурс с TLS/mTLS и источниками заголовков; ключи/пароли передаются только через secrets.", false, false, false, func(ctx context.Context, in ResourceCreate) (resources.Resource, error) {
		return e.Resources.SaveResource(ctx, "", in.EnvironmentID, in.Name, in.Kind, 0, in.Config, in.Secrets.domain())
	})
	addTool(s, "qag_resource_update", "Обновить полную конфигурацию ресурса по revision; secrets.replace/clear не раскрываются в ответе.", false, false, false, func(ctx context.Context, in ResourceUpdate) (resources.Resource, error) {
		return e.Resources.SaveResource(ctx, in.ID, "", in.Name, in.Kind, in.Revision, in.Config, in.Secrets.domain())
	})
	addTool(s, "qag_proto_bundle_upload", "Сохранить proto bundle как map относительных имён к текстам; пути файлов сервера не читаются.", false, false, false, func(ctx context.Context, in ProtoUpload) (resources.Resource, error) {
		return e.Resources.UploadProto(ctx, in.ID, in.Revision, in.Files)
	})
	addTool(s, "qag_runs_list", "Текущие/сохранённые/несохранённые прогоны кейса, новые первыми; retained/active/cursor.", true, false, false, func(ctx context.Context, in RunsList) (catalog.Page[runs.Run], error) {
		limit, err := page(in.Page)
		if err != nil {
			return catalog.Page[runs.Run]{}, err
		}
		return e.Runs.List(ctx, in.TestCaseID, in.Retained, limit, in.Cursor, in.Active)
	})
	addTool(s, "qag_run_start", "Создать прогон снимка кейса и окружения; executeAll запускает очередь исходных вызовов. Для повтора используйте тот же UUID idempotencyKey.", false, true, true, func(ctx context.Context, in RunStart) (runs.Run, error) {
		return e.StartRun(ctx, in.TestCaseID, in.EnvironmentID, in.Revision, in.IdempotencyKey, in.ExecuteAll)
	})
	addTool(s, "qag_run_get", "Снимок, шаги, очередь, все варианты/версии/попытки и причины сохранения без повторной сети.", true, false, false, func(ctx context.Context, in IDInput) (runs.Run, error) { return e.GetRun(ctx, in.ID) })
	addTool(s, "qag_run_step_update", "Записать ручной status not_run/passed/failed и комментарий по revision шага.", false, false, false, func(ctx context.Context, in RunStepUpdate) (runs.Run, error) {
		return e.Runs.UpdateStep(ctx, in.RunID, in.StepID, in.Status, in.Comment, in.Revision)
	})
	addTool(s, "qag_run_steps_pass_all", "Атомарно отметить все шаги passed по полному набору stepId/revision; комментарии и результаты сохраняются, сеть не запускается.", false, false, false, func(ctx context.Context, in RunPassAll) (runs.Run, error) {
		return e.Runs.PassAll(ctx, in.RunID, in.Steps)
	})
	addTool(s, "qag_run_finish", "Завершить прогон с официальным итогом; активные попытки/очередь запрещают завершение.", false, false, false, func(ctx context.Context, in IDInput) (runs.Run, error) { return e.FinishRun(ctx, in.ID) })
	addTool(s, "qag_run_cancel", "Явно отменить прогон, очередь, источники и попытки. Выполненное внешнее действие не откатывается.", false, true, false, func(ctx context.Context, in IDInput) (runs.Run, error) { return e.CancelRun(ctx, in.ID) })
	addTool(s, "qag_run_save", "Закрепить завершённый результат в сохранённой истории.", false, false, false, func(ctx context.Context, in IDInput) (runs.Run, error) { return e.Runs.Save(ctx, in.ID) })
	addTool(s, "qag_run_delete", "Удалить допустимый несохранённый терминальный прогон; закреплённые результаты защищены.", false, false, false, func(ctx context.Context, in IDInput) (Deleted, error) {
		err := e.Runs.Delete(ctx, in.ID)
		return Deleted{Deleted: err == nil}, err
	})
	addTool(s, "qag_batch_start", "Последовательно выполнить все исходные блоки активного прогона. Повтор команды — с тем же idempotencyKey.", false, true, true, func(ctx context.Context, in BatchStart) (runs.Batch, error) {
		return e.StartBatch(ctx, in.RunID, in.IdempotencyKey)
	})
	addTool(s, "qag_editor_attempt_start", "Пробно выполнить исходный блок редактора без создания прогона. Явные environmentId/revision/idempotencyKey обязательны.", false, true, true, func(ctx context.Context, in EditorAttemptStart) (execution.Attempt, error) {
		return e.EditorAttempt(ctx, in.TestCaseID, in.BlockID, in.EnvironmentID, in.Revision, in.IdempotencyKey)
	})
	addTool(s, "qag_run_attempt_start", "Выполнить исходный блок снимка прогона, сохранив отдельную попытку и сравнение.", false, true, true, func(ctx context.Context, in RunAttemptStart) (execution.Attempt, error) {
		return e.RunAttempt(ctx, in.RunID, in.BlockID, in.IdempotencyKey)
	})
	addTool(s, "qag_attempt_get", "Сохранённое состояние, фактический ответ и сравнение исходной попытки; чтение не повторяет запрос.", true, false, false, func(ctx context.Context, in IDInput) (execution.Attempt, error) { return e.GetAttempt(ctx, in.ID) })
	addTool(s, "qag_attempt_cancel", "Явно отменить отдельную попытку; проверьте целевую систему перед новой попыткой.", false, true, false, func(ctx context.Context, in IDInput) (execution.Attempt, error) { return e.CancelAttempt(ctx, in.ID) })
	addTool(s, "qag_variant_create", "Создать исследовательскую вкладку у исходного блока активного прогона без ожидаемого результата.", false, false, false, func(ctx context.Context, in VariantCreate) (runs.Variant, error) {
		return e.Runs.SaveVariant(ctx, in.RunID, in.BlockID, "", in.Name, 0, nil)
	})
	addTool(s, "qag_variant_update", "Сохранить request-only версию варианта; ожидаемые данные и изменение родительских kind/resource не принимаются.", false, false, false, func(ctx context.Context, in VariantUpdate) (runs.Variant, error) {
		req := catalog.CallRequest{TimeoutSeconds: in.Request.TimeoutSeconds, HTTP: in.Request.HTTP, GRPC: in.Request.GRPC, SQL: in.Request.SQL}
		return e.Runs.SaveVariant(ctx, in.RunID, "", in.VariantID, in.Name, in.Revision, &req)
	})
	addTool(s, "qag_variant_attempt_start", "Выполнить сохранённую revision исследовательского варианта без ожидания и сравнения; исходный итог сохраняется.", false, true, true, func(ctx context.Context, in VariantAttemptStart) (execution.Attempt, error) {
		return e.VariantAttempt(ctx, in.RunID, in.VariantID, in.Revision, in.IdempotencyKey)
	})
	addTool(s, "qag_report_prepare", "Подготовить согласованный публичный срез отчёта, includeExpected по умолчанию true; не выполняет вызовы.", true, false, false, func(ctx context.Context, in ReportPrepare) (runs.Report, error) {
		expected := true
		if in.IncludeExpected != nil {
			expected = *in.IncludeExpected
		}
		return s.options.Reports.Prepare(ctx, in.RunID, expected)
	})
	addTool(s, "qag_report_export", "Получить pdf/text/markdown/rich для подготовленного snapshotId/hash с тем же includeExpected; rich — переносимые HTML/RTF/text с PNG.", true, false, false, func(ctx context.Context, in ReportExport) (ExportResult, error) {
		files, err := s.export(ctx, in)
		return ExportResult{Files: files}, err
	})
	addTool(s, "qag_report_copy", "Явно копировать text/markdown/rich на доступном native clipboard. Иначе copied=false и файлы для clipboard клиента.", false, false, false, s.copyReport)
	addTool(s, "qag_artifact_read", "Читать только выданный артефакт по byte offset, до 256 KiB за запрос; base64 позволяет собрать UTF-8 без потерь.", true, false, false, func(ctx context.Context, in ArtifactRead) (ArtifactChunk, error) {
		return s.Artifacts.Chunk(in.ID, in.Offset, in.Length)
	})
}
func (s *Server) export(ctx context.Context, in ReportExport) ([]Artifact, error) {
	files, err := s.options.Reports.Export(ctx, in.Reference, in.Format)
	if err != nil {
		return nil, err
	}
	data := make([]File, 0, len(files))
	for _, f := range files {
		data = append(data, File{Name: f.Name, MIMEType: f.MIMEType, Data: f.Data})
	}
	return s.Artifacts.PutGroup(data)
}
func (s *Server) copyReport(ctx context.Context, in ReportExport) (CopyResult, error) {
	if in.Format != "rich" && in.Format != "text" && in.Format != "markdown" {
		return CopyResult{}, problem.Invalid("format", "Для копирования выберите text, markdown или rich")
	}
	files, err := s.options.Reports.Export(ctx, in.Reference, in.Format)
	if err != nil {
		return CopyResult{}, err
	}
	data := []File{}
	content := reporting.RichContent{}
	for _, f := range files {
		data = append(data, File{Name: f.Name, MIMEType: f.MIMEType, Data: f.Data})
		switch f.MIMEType {
		case "application/rtf":
			content.RTF = string(f.Data)
		case "text/html; charset=utf-8":
			content.HTML = string(f.Data)
		default:
			content.Text = string(f.Data)
		}
	}
	artifacts, err := s.Artifacts.PutGroup(data)
	if err != nil {
		return CopyResult{}, err
	}
	out := CopyResult{Files: artifacts}
	if !clipboard.Available() {
		out.Code = "clipboard_unavailable"
		out.Message = "Скопируйте полученные файлы средствами MCP-клиента; clipboard хоста недоступен контейнеру"
		return out, nil
	}
	if in.Format == "rich" {
		err = clipboard.Write(content)
	} else {
		err = clipboard.WriteText(content.Text)
	}
	if err != nil {
		out.Code = "clipboard_error"
		out.Message = "Не удалось записать clipboard; подготовленные файлы доступны"
		return out, nil
	}
	out.Copied = true
	return out, nil
}
