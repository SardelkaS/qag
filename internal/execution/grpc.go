package execution

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/bufbuild/protocompile"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	reflection "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	"github.com/SardelkaS/qag/internal/catalog"
	"github.com/SardelkaS/qag/internal/resources"
)

type Runner struct {
	headers map[string]*headerScope
	mu      sync.Mutex
	cache   map[string]*protoregistry.Files
	order   []string
}

func NewRunner() *Runner {
	return &Runner{cache: map[string]*protoregistry.Files{}, headers: map[string]*headerScope{}}
}

func (r *Runner) grpc(ctx context.Context, scope string, b catalog.Block, res resources.Resolved) (result Result, err error) {
	result = Result{Protocol: "grpc"}
	if err := checkRequestGate(ctx); err != nil {
		return result, err
	}
	requestHeaders := []catalog.KeyValue{}
	for _, h := range mergedHeaders(res.Resource.Config.Headers, b.GRPC.Metadata) {
		requestHeaders = append(requestHeaders, catalog.KeyValue{Name: strings.ToLower(h.Name), Value: h.Value})
	}
	sent, finish, err := beginExchange(ctx, LogRequest{Protocol: "grpc", Address: res.Resource.Config.Address, Method: "/" + b.GRPC.Service + "/" + b.GRPC.Method, Headers: requestHeaders, Body: b.GRPC.Body}, res.Secrets)
	if err != nil {
		return result, err
	}
	defer func() {
		if e := finish(result, err); e != nil {
			err = e
		}
	}()
	if err = checkRequestGate(ctx); err != nil {
		return result, err
	}
	tlsConfig, err := resources.TLS(res.Resource.Config, res.Secrets)
	if err != nil {
		return result, err
	}
	var creds credentials.TransportCredentials = insecure.NewCredentials()
	if tlsConfig != nil {
		creds = credentials.NewTLS(tlsConfig)
	}
	conn, err := grpc.DialContext(ctx, "passthrough:///"+res.Resource.Config.Address, grpc.WithTransportCredentials(creds), grpc.WithDisableRetry(), grpc.WithReturnConnectionError(), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxResponseBytes+1), grpc.MaxCallSendMsgSize(32<<20)))
	if err != nil {
		result.StatusMessage = err.Error()
		return result, err
	}
	defer conn.Close()
	md := metadata.MD{}
	for _, h := range mergedHeaders(res.Resource.Config.Headers, b.GRPC.Metadata) {
		md.Append(strings.ToLower(h.Name), h.Value)
	}
	ctx = metadata.NewOutgoingContext(ctx, md)
	key := scope + ":" + res.Resource.VersionID + ":" + b.GRPC.Service
	r.mu.Lock()
	files := r.cache[key]
	r.mu.Unlock()
	if files == nil {
		if len(res.Resource.Config.ProtoFiles) > 0 {
			files, err = compileFiles(ctx, res.Resource.Config.ProtoFiles)
		} else {
			files, err = reflectionFiles(ctx, conn, b.GRPC.Service)
		}
		if err != nil {
			return result, fmt.Errorf("не удалось загрузить описание gRPC: %w", err)
		}
		r.mu.Lock()
		if len(r.order) >= 64 {
			delete(r.cache, r.order[0])
			r.order = r.order[1:]
		}
		if r.cache[key] == nil {
			r.order = append(r.order, key)
		}
		r.cache[key] = files
		r.mu.Unlock()
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(b.GRPC.Service))
	if err != nil {
		return result, fmt.Errorf("сервис не найден в описании")
	}
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return result, fmt.Errorf("указано не имя сервиса")
	}
	method := service.Methods().ByName(protoreflect.Name(b.GRPC.Method))
	if method == nil {
		return result, fmt.Errorf("метод не найден в описании")
	}
	if method.IsStreamingClient() || method.IsStreamingServer() {
		return result, fmt.Errorf("поддерживаются только unary gRPC-методы")
	}
	request, response := dynamicpb.NewMessage(method.Input()), dynamicpb.NewMessage(method.Output())
	resolver := dynamicpb.NewTypes(files)
	if err = (protojson.UnmarshalOptions{Resolver: resolver}).Unmarshal([]byte(b.GRPC.Body), request); err != nil {
		return result, fmt.Errorf("сообщение не соответствует описанию: %w", err)
	}
	callCtx := ctx
	var initial, trailers metadata.MD
	if err = checkRequestGate(ctx); err != nil {
		return result, err
	}
	sent()
	err = conn.Invoke(callCtx, "/"+b.GRPC.Service+"/"+b.GRPC.Method, request, response, grpc.Header(&initial), grpc.Trailer(&trailers))
	result.Headers = headers(initial)
	result.Trailers = headers(trailers)
	result.GRPCCode = grpcCode(status.Code(err))
	if err != nil {
		result.StatusMessage = status.Convert(err).Message()
		if strings.Contains(result.StatusMessage, "received message larger than max") {
			result.Truncated = true
			return result, nil
		}
		if len(initial) == 0 && len(trailers) == 0 {
			return result, err
		}
		return result, nil
	}
	body, err := (protojson.MarshalOptions{Resolver: resolver, EmitUnpopulated: true}).Marshal(response)
	if len(body) > MaxResponseBytes {
		body = body[:MaxResponseBytes]
		result.Truncated = true
	}
	result.Body = string(body)
	return result, err
}

func grpcCode(code codes.Code) string {
	names := []string{"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"}
	if int(code) < len(names) {
		return names[int(code)]
	}
	return "UNKNOWN"
}

func compileFiles(ctx context.Context, source map[string]string) (*protoregistry.Files, error) {
	names := []string{}
	for name := range source {
		names = append(names, name)
	}
	sort.Strings(names)
	compiler := protocompile.Compiler{Resolver: protocompile.WithStandardImports(&protocompile.SourceResolver{Accessor: protocompile.SourceAccessorFromMap(source)}), MaxParallelism: 2}
	compiled, err := compiler.Compile(ctx, names...)
	if err != nil {
		return nil, err
	}
	files := new(protoregistry.Files)
	seen := map[string]bool{}
	var register func(protoreflect.FileDescriptor) error
	register = func(file protoreflect.FileDescriptor) error {
		if seen[file.Path()] {
			return nil
		}
		seen[file.Path()] = true
		for i := 0; i < file.Imports().Len(); i++ {
			if err := register(file.Imports().Get(i).FileDescriptor); err != nil {
				return err
			}
		}
		return files.RegisterFile(file)
	}
	for _, file := range compiled {
		if err = register(file); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func reflectionFiles(ctx context.Context, conn *grpc.ClientConn, service string) (*protoregistry.Files, error) {
	var last error
	for _, version := range []string{"v1", "v1alpha"} {
		files, err := reflectionVersion(ctx, conn, service, version)
		if err == nil {
			return files, nil
		}
		last = err
		if status.Code(err) != codes.Unimplemented {
			break
		}
	}
	return nil, last
}
func reflectionVersion(ctx context.Context, conn *grpc.ClientConn, service, version string) (*protoregistry.Files, error) {
	stream, err := conn.NewStream(ctx, &grpc.StreamDesc{ServerStreams: true, ClientStreams: true}, "/grpc.reflection."+version+".ServerReflection/ServerReflectionInfo")
	if err != nil {
		return nil, err
	}
	defer stream.CloseSend()
	files := map[string]*descriptorpb.FileDescriptorProto{}
	size := 0
	fetch := func(request *reflection.ServerReflectionRequest) error {
		if err := stream.SendMsg(request); err != nil {
			return err
		}
		response := new(reflection.ServerReflectionResponse)
		if err := stream.RecvMsg(response); err != nil {
			return err
		}
		if failure := response.GetErrorResponse(); failure != nil {
			return status.Error(codes.Code(failure.ErrorCode), failure.ErrorMessage)
		}
		for _, data := range response.GetFileDescriptorResponse().GetFileDescriptorProto() {
			file := new(descriptorpb.FileDescriptorProto)
			if err := proto.Unmarshal(data, file); err != nil {
				return err
			}
			if files[file.GetName()] == nil {
				size += len(data)
				files[file.GetName()] = file
			}
			if len(files) > 100 || size > 5<<20 {
				return fmt.Errorf("описание gRPC превышает лимит 100 файлов / 5 MiB")
			}
		}
		return nil
	}
	if err = fetch(&reflection.ServerReflectionRequest{MessageRequest: &reflection.ServerReflectionRequest_FileContainingSymbol{FileContainingSymbol: service}}); err != nil {
		return nil, err
	}
	for {
		missing := map[string]bool{}
		for _, file := range files {
			for _, dependency := range file.Dependency {
				if files[dependency] == nil {
					missing[dependency] = true
				}
			}
		}
		if len(missing) == 0 {
			break
		}
		before := len(files)
		for dependency := range missing {
			if err = fetch(&reflection.ServerReflectionRequest{MessageRequest: &reflection.ServerReflectionRequest_FileByFilename{FileByFilename: dependency}}); err != nil {
				return nil, err
			}
		}
		if len(files) == before {
			return nil, fmt.Errorf("не удалось получить импортированные proto-файлы")
		}
	}
	set := &descriptorpb.FileDescriptorSet{}
	names := []string{}
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		set.File = append(set.File, files[name])
	}
	return protodesc.NewFiles(set)
}
