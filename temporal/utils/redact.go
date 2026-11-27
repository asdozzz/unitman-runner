package utils

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strings"
)

// RedactedValue — единая заглушка для скрытых учётных данных.
const RedactedValue = "***REDACTED***"

var (
	// urlUserinfoPattern находит userinfo в URL: https://user:pass@host.
	urlUserinfoPattern = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://)[^/@\s]*@`)
	// oauth2CredentialPattern находит «голые» oauth2:-креденшелы вне URL.
	oauth2CredentialPattern = regexp.MustCompile(`(?i)oauth2:[^\s@]+`)
	// bearerPattern находит Bearer-токены.
	bearerPattern = regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=\-]+`)
	// authorizationPattern находит значение заголовка Authorization.
	authorizationPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*)[^\r\n]+`)
	// sensitiveQueryPattern находит секретные query-параметры.
	sensitiveQueryPattern = regexp.MustCompile(`(?i)([?&](?:token|access_token|private_token|api_key|client_secret)=)[^&#\s]*`)
)

// sensitiveKeyMarkers — подстроки имени поля (без регистра, разделителей),
// значения которых всегда скрываются.
var sensitiveKeyMarkers = []string{
	"token",
	"apikey",
	"password",
	"passwd",
	"secret",
	"authorization",
	"credential",
	"cipher",
}

// RedactString применяет все текстовые правила редактирования к строке.
// Функция идемпотентна и безопасна для произвольного текста.
func RedactString(value string) string {
	if value == "" {
		return ""
	}

	redacted := sensitiveQueryPattern.ReplaceAllString(value, "${1}"+RedactedValue)
	redacted = urlUserinfoPattern.ReplaceAllString(redacted, "${1}"+RedactedValue+"@")
	redacted = authorizationPattern.ReplaceAllString(redacted, "${1}"+RedactedValue)
	redacted = bearerPattern.ReplaceAllString(redacted, "${1}"+RedactedValue)
	redacted = oauth2CredentialPattern.ReplaceAllString(redacted, "oauth2:"+RedactedValue)

	return redacted
}

// RedactURL скрывает userinfo и секретные query-параметры в URL.
// Fail-closed: если URL не разбирается, возвращается полностью скрытое значение.
func RedactURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return RedactedValue
	}

	// scp-подобные адреса (git@host:org/repo.git) не парсятся как URL —
	// скрываем целиком, чтобы не залогировать userinfo.
	if parsed.Scheme == "" && strings.Contains(trimmed, "@") {
		return RedactedValue
	}

	if parsed.User != nil {
		parsed.User = url.User(RedactedValue)
	}

	return RedactString(parsed.String())
}

// RedactFields рекурсивно редактирует набор log-полей по имени ключа и значению.
func RedactFields(fields map[string]any) map[string]any {
	if fields == nil {
		return nil
	}

	out := make(map[string]any, len(fields))
	for key, value := range fields {
		switch {
		case isStorageURLKey(key):
			if str, ok := value.(string); ok {
				out[key] = RedactURL(str)
			} else {
				out[key] = RedactedValue
			}
		case isSensitiveKey(key):
			out[key] = RedactedValue
		default:
			out[key] = RedactValue(value)
		}
	}

	return out
}

// RedactValue рекурсивно редактирует произвольное значение (struct/map/slice/
// string), пригодное для структурированного логирования. Поля с чувствительными
// именами и StorageUrl скрываются независимо от формы значения.
func RedactValue(value any) any {
	return redactValue(reflect.ValueOf(value), 0)
}

const maxRedactDepth = 32

func redactValue(v reflect.Value, depth int) any {
	if !v.IsValid() {
		return nil
	}
	if depth > maxRedactDepth {
		return RedactedValue
	}

	switch v.Kind() {
	case reflect.Interface, reflect.Ptr:
		if v.IsNil() {
			return nil
		}
		return redactValue(v.Elem(), depth+1)

	case reflect.String:
		return RedactString(v.String())

	case reflect.Struct:
		out := make(map[string]any, v.NumField())
		typ := v.Type()
		sensitiveValue := structCarriesSensitiveKey(v, typ)
		for i := 0; i < v.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				// Неэкспортированное поле нельзя прочитать — не логируем.
				continue
			}

			name := jsonFieldName(field)
			fieldValue := v.Field(i)
			if !fieldValue.CanInterface() {
				continue
			}

			switch {
			case isStorageURLKey(name):
				out[name] = redactStorageURLValue(fieldValue)
			case isSensitiveKey(name):
				out[name] = RedactedValue
			case sensitiveValue && isValueKey(name):
				out[name] = RedactedValue
			default:
				out[name] = redactValue(fieldValue, depth+1)
			}
		}
		return out

	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		sensitiveValue := mapCarriesSensitiveKey(v)
		iter := v.MapRange()
		for iter.Next() {
			key := fmt.Sprint(iter.Key().Interface())
			mapValue := iter.Value()
			switch {
			case isStorageURLKey(key):
				out[key] = redactStorageURLValue(mapValue)
			case isSensitiveKey(key):
				out[key] = RedactedValue
			case sensitiveValue && isValueKey(key):
				out[key] = RedactedValue
			default:
				out[key] = redactValue(mapValue, depth+1)
			}
		}
		return out

	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := 0; i < v.Len(); i++ {
			out[i] = redactValue(v.Index(i), depth+1)
		}
		return out

	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	case reflect.Float32, reflect.Float64:
		return v.Float()
	default:
		if v.CanInterface() {
			return v.Interface()
		}
		return RedactedValue
	}
}

// redactStorageURLValue применяет URL-редактирование к значению StorageUrl,
// а нестроковые значения скрывает целиком.
func redactStorageURLValue(v reflect.Value) any {
	if v.Kind() == reflect.String {
		return RedactURL(v.String())
	}
	return RedactedValue
}

// isValueKey определяет ключи, содержащие само значение переменной.
func isValueKey(key string) bool {
	switch compactKey(key) {
	case "value", "val":
		return true
	default:
		return false
	}
}

// isVariableNameKey определяет ключи-идентификаторы переменной.
func isVariableNameKey(key string) bool {
	switch compactKey(key) {
	case "id", "name", "key", "field", "variable":
		return true
	default:
		return false
	}
}

// structCarriesSensitiveKey сообщает, есть ли у структуры поле-идентификатор
// (id/name/key), значение которого выглядит чувствительным. В этом случае
// парное поле value скрывается, чтобы значение переменной не утекло.
func structCarriesSensitiveKey(v reflect.Value, typ reflect.Type) bool {
	for i := 0; i < v.NumField(); i++ {
		field := typ.Field(i)
		if field.PkgPath != "" {
			continue
		}
		if !isVariableNameKey(jsonFieldName(field)) {
			continue
		}
		fieldValue := unwrapValue(v.Field(i))
		if fieldValue.Kind() == reflect.String && isSensitiveKey(fieldValue.String()) {
			return true
		}
	}
	return false
}

// mapCarriesSensitiveKey — аналог structCarriesSensitiveKey для map.
func mapCarriesSensitiveKey(v reflect.Value) bool {
	iter := v.MapRange()
	for iter.Next() {
		key := fmt.Sprint(iter.Key().Interface())
		if !isVariableNameKey(key) {
			continue
		}
		mapValue := unwrapValue(iter.Value())
		if mapValue.IsValid() && mapValue.Kind() == reflect.String && isSensitiveKey(mapValue.String()) {
			return true
		}
	}
	return false
}

// unwrapValue снимает обёртки interface/ptr, чтобы проверить строковое значение.
func unwrapValue(v reflect.Value) reflect.Value {
	for v.IsValid() && (v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr) {
		if v.IsNil() {
			return reflect.Value{}
		}
		v = v.Elem()
	}
	return v
}

// jsonFieldName возвращает имя поля по json-тегу либо имя Go-поля.
func jsonFieldName(field reflect.StructField) string {
	tag := field.Tag.Get("json")
	if tag == "" {
		return field.Name
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return field.Name
	}
	return name
}

// compactKey приводит имя поля/параметра к виду без разделителей и регистра.
func compactKey(key string) string {
	compact := strings.ToLower(strings.TrimSpace(key))
	replacer := strings.NewReplacer("_", "", "-", "", ".", "", " ", "")
	return replacer.Replace(compact)
}

// isStorageURLKey определяет поля/ключи, содержащие URL репозитория.
func isStorageURLKey(key string) bool {
	switch compactKey(key) {
	case "storageurl", "storageuri", "repositoryurl", "repourl", "giturl", "remoteurl":
		return true
	default:
		return false
	}
}

// isSensitiveKey определяет поля/ключи, значения которых скрываются целиком.
func isSensitiveKey(key string) bool {
	compact := compactKey(key)
	if compact == "" {
		return false
	}

	if isStorageURLKey(key) {
		return true
	}

	for _, marker := range sensitiveKeyMarkers {
		if strings.Contains(compact, marker) {
			return true
		}
	}

	return false
}

// defaultLogger — структурированный логгер раннера. Пишет JSON в stdout,
// как и прежние fmt.Println-сообщения активностей.
var defaultLogger = slog.New(slog.NewJSONHandler(os.Stdout, nil))

// LogActivityCommand логирует команду активности через стандартный логгер.
// Значение команды проходит централизованное рекурсивное редактирование,
// поэтому токены не могут попасть в лог.
func LogActivityCommand(label string, command any) {
	LogCommand(defaultLogger, label, command)
}

// LogCommand логирует команду активности через переданный логгер. Полезная
// нагрузка всегда проходит через RedactValue.
func LogCommand(logger *slog.Logger, label string, command any) {
	if logger == nil {
		logger = defaultLogger
	}
	logger.Info(label, slog.Any("command", RedactValue(command)))
}
