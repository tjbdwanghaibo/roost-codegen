package dao

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"go/format"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"
	"unicode"
)

func generateDao(dao DaoDef, defs *Definitions, pkg string, outFile string, force bool) (bool, error) {
	if err := validateDatabaseScope(dao); err != nil {
		return false, err
	}
	if err := validateGeneratedStorageFields(dao.Name, dao.Fields, "id", "tracker", "persistPatchSet", "persistPatchUnset", "persistPatchFull"); err != nil {
		return false, err
	}
	if err := validateGeneratedMapFields(dao.Fields); err != nil {
		return false, err
	}
	tmpl, err := template.New("dao").Funcs(funcMap(defs)).Parse(daoTemplate)
	if err != nil {
		return false, fmt.Errorf("template parse: %w", err)
	}

	data := daoTmplData{
		Package: pkg,
		Dao:     dao,
		Defs:    defs,
		HasMaps: hasMapFields(dao.Fields),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return false, fmt.Errorf("template exec: %w", err)
	}

	content, err := format.Source(buf.Bytes())
	if err != nil {
		return false, fmt.Errorf("format generated dao: %w", err)
	}
	return writeIfChanged(content, outFile, force)
}

func generateNested(nested NestedDef, pkg string, outFile string, force bool) (bool, error) {
	if err := validateGeneratedStorageFields(nested.Name, nested.Fields); err != nil {
		return false, err
	}
	if err := validateGeneratedMapFields(nested.Fields); err != nil {
		return false, err
	}
	tmpl, err := template.New("nested").Funcs(template.FuncMap{
		"snakeCase":  toSnake,
		"lower1":     lower1,
		"fieldType":  fieldType,
		"fieldVar":   fieldVarName,
		"mapValType": mapValType,
		"mapNewExpr": mapNewExpr,
		"rawMapType": rawMapType,
		"hasMaps":    hasMapFields,
	}).Parse(nestedTemplate)
	if err != nil {
		return false, fmt.Errorf("template parse: %w", err)
	}

	data := nestedTmplData{
		Package: pkg,
		Nested:  nested,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return false, fmt.Errorf("template exec: %w", err)
	}

	content, err := format.Source(buf.Bytes())
	if err != nil {
		return false, fmt.Errorf("format generated nested: %w", err)
	}
	return writeIfChanged(content, outFile, force)
}

func generateRedisDao(dao RedisDaoDef, pkg string, outFile string, force bool) (bool, error) {
	if dao.Mode == "" {
		dao.Mode = "ref-hmap"
	}
	if dao.Mode != "ref-hmap" && dao.Mode != "raw" {
		return false, fmt.Errorf("redis dao %s has unsupported mode %q", dao.Name, dao.Mode)
	}
	if dao.Key == "" {
		return false, fmt.Errorf("redis dao %s missing key", dao.Name)
	}
	if dao.KeyType == "" {
		return false, fmt.Errorf("redis dao %s missing key type for key %s", dao.Name, dao.Key)
	}
	defaultTTL, err := redisDaoDefaultTTL(dao)
	if err != nil {
		return false, err
	}
	tmpl, err := template.New("redis_dao").Funcs(template.FuncMap{
		"lower1":   lower1,
		"goString": strconv.Quote,
		"isRaw":    func(mode string) bool { return mode == "raw" },
		"isRefMap": func(mode string) bool { return mode == "" || mode == "ref-hmap" },
	}).Parse(redisDaoTemplate)
	if err != nil {
		return false, fmt.Errorf("template parse: %w", err)
	}

	data := redisDaoTmplData{
		Package:       pkg,
		Dao:           dao,
		DefaultPrefix: redisDaoDefaultPrefix(dao),
		RedisName:     redisDaoName(dao),
		DefaultTTL:    defaultTTL,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return false, fmt.Errorf("template exec: %w", err)
	}

	content, err := format.Source(buf.Bytes())
	if err != nil {
		return false, fmt.Errorf("format generated redis dao: %w", err)
	}
	return writeIfChanged(content, outFile, force)
}

func writeIfChanged(content []byte, outFile string, force bool) (bool, error) {
	if !force {
		existing, err := os.ReadFile(outFile)
		if err == nil {
			if fmt.Sprintf("%x", md5.Sum(existing)) == fmt.Sprintf("%x", md5.Sum(content)) {
				return false, nil
			}
		}
	}
	return true, os.WriteFile(outFile, content, 0644)
}

func funcMap(defs *Definitions) template.FuncMap {
	return template.FuncMap{
		"snakeCase":     toSnake,
		"lower1":        lower1,
		"daoDBConst":    daoDBConstName,
		"daoCollConst":  daoCollectionConstName,
		"dbScope":       databaseScopeExpr,
		"isNested":      func(typeName string) bool { return isNestedType(defs, typeName) },
		"persistFields": func(fields []FieldDef) []FieldDef { return filterPersist(fields) },
		"syncFields":    func(fields []FieldDef) []FieldDef { return filterSync(fields) },
		"dirtyFields":   func(fields []FieldDef) []FieldDef { return filterDirty(fields) },
		"bsonKey":       func(name string) string { return toSnake(name) },
		"fieldMaskName": fieldMaskName,
		"dirtyScope":    dirtyScopeExpr,
		"fieldType":     fieldType,
		"fieldVar":      fieldVarName,
		"mapValType":    mapValType,
		"mapNewExpr":    mapNewExpr,
		"rawMapType":    rawMapType,
		"mapHelperName": mapHelperName,
		"hasMaps":       hasMapFields,
	}
}

func validateDatabaseScope(dao DaoDef) error {
	if dao.DbScope == "" || dao.DbScope == "global" || dao.DbScope == "sid" {
		return nil
	}
	return fmt.Errorf("dao %s has unsupported dbscope %q (want global or sid)", dao.Name, dao.DbScope)
}

func databaseScopeExpr(dao DaoDef) string {
	if dao.DbScope == "sid" {
		return "checkpoint.DatabaseScopeServer"
	}
	return "checkpoint.DatabaseScopeGlobal"
}

func fieldMaskName(daoName, fieldName string) string {
	return lower1(daoName) + "Field" + fieldName
}

func daoDBConstName(daoName string) string {
	return derefDaoTypeName(daoName) + "DBName"
}

func daoCollectionConstName(daoName string) string {
	return derefDaoTypeName(daoName) + "Collection"
}

func derefDaoTypeName(typeName string) string {
	typeName = strings.TrimPrefix(typeName, "*")
	if idx := strings.LastIndex(typeName, "."); idx >= 0 {
		typeName = typeName[idx+1:]
	}
	return typeName
}

func dirtyScopeExpr(f FieldDef) string {
	switch {
	case f.Tag.Persist && f.Tag.Sync:
		return "checkpoint.DirtyPersist | checkpoint.DirtySync"
	case f.Tag.Persist:
		return "checkpoint.DirtyPersist"
	case f.Tag.Sync:
		return "checkpoint.DirtySync"
	default:
		return "0"
	}
}

func fieldType(f FieldDef) string {
	if f.Kind == KindMap {
		switch f.Tag.Map {
		case "fast":
			return "*fmap.FastMap[" + f.MapKey + ", " + mapValType(f) + "]"
		case "sharded":
			return "*fmap.ShardedSafeMap[" + f.MapKey + ", " + mapValType(f) + "]"
		default:
			return "*fmap.SmallSafeMap[" + f.MapKey + ", " + mapValType(f) + "]"
		}
	}
	return f.TypeStr
}

func mapNewExpr(f FieldDef, capExpr string) string {
	keyType := f.MapKey
	valType := mapValType(f)
	switch f.Tag.Map {
	case "fast":
		return "fmap.NewFastMap[" + keyType + ", " + valType + "](" + capExpr + ", " + mapHashExpr(keyType) + ")"
	case "sharded":
		return "fmap.NewShardedSafeMap[" + keyType + ", " + valType + "](" + shardCountExpr(capExpr) + ", " + mapHashExpr(keyType) + ")"
	default:
		return "fmap.NewSmallSafeMap[" + keyType + ", " + valType + "](" + capExpr + ")"
	}
}

func mapHashExpr(keyType string) string {
	if keyType == "string" {
		return "fmap.HashString"
	}
	if isIntegerType(keyType) {
		return "fmap.HashInteger[" + keyType + "]"
	}
	return "nil"
}

func validateGeneratedMapFields(fields []FieldDef) error {
	for _, f := range fields {
		if f.Kind != KindMap {
			continue
		}
		switch f.Tag.Map {
		case "", "small":
			continue
		case "fast", "sharded":
			if f.MapKey == "string" || isIntegerType(f.MapKey) {
				continue
			}
			return fmt.Errorf("dao map field %s uses map=%s with unsupported key type %s", f.Name, f.Tag.Map, f.MapKey)
		default:
			return fmt.Errorf("dao map field %s has unknown map kind %q", f.Name, f.Tag.Map)
		}
	}
	return nil
}

func validateGeneratedStorageFields(typeName string, fields []FieldDef, reserved ...string) error {
	owners := make(map[string]string, len(fields)+len(reserved))
	for _, name := range reserved {
		owners[name] = "framework field " + name
	}
	for _, field := range fields {
		name := fieldVarName(field.Name)
		if owner, exists := owners[name]; exists {
			return fmt.Errorf("dao %s field %s generates private storage name %q, which conflicts with %s", typeName, field.Name, name, owner)
		}
		owners[name] = "field " + field.Name
	}
	return nil
}

func shardCountExpr(capExpr string) string {
	if capExpr == "0" {
		return "32"
	}
	return "32"
}

func mapValType(f FieldDef) string {
	if f.IsPtr {
		return "*" + f.MapVal
	}
	return f.MapVal
}

func rawMapType(f FieldDef) string {
	return "map[" + f.MapKey + "]" + mapValType(f)
}

func mapHelperName(daoName, fieldName string) string {
	return lower1(daoName) + fieldName + "RawMap"
}

func hasMapFields(fields []FieldDef) bool {
	for _, f := range fields {
		if f.Kind == KindMap {
			return true
		}
	}
	return false
}

func filterPersist(fields []FieldDef) []FieldDef {
	var out []FieldDef
	for _, f := range fields {
		if f.Tag.Persist {
			out = append(out, f)
		}
	}
	return out
}

func filterSync(fields []FieldDef) []FieldDef {
	var out []FieldDef
	for _, f := range fields {
		if f.Tag.Sync {
			out = append(out, f)
		}
	}
	return out
}

func filterDirty(fields []FieldDef) []FieldDef {
	var out []FieldDef
	for _, f := range fields {
		if f.Tag.Persist || f.Tag.Sync {
			out = append(out, f)
		}
	}
	return out
}

func isNestedType(defs *Definitions, typeName string) bool {
	typeName = strings.TrimPrefix(typeName, "*")
	for _, n := range defs.Nested {
		if n.Name == typeName {
			return true
		}
	}
	return false
}

func lower1(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func fieldVarName(name string) string {
	if name == "" {
		return ""
	}
	runes := []rune(name)
	end := 1
	for end < len(runes) && unicode.IsUpper(runes[end]) {
		if end+1 < len(runes) && unicode.IsLower(runes[end+1]) {
			break
		}
		end++
	}
	for i := 0; i < end; i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

type daoTmplData struct {
	Package string
	Dao     DaoDef
	Defs    *Definitions
	HasMaps bool
}

type nestedTmplData struct {
	Package string
	Nested  NestedDef
}

type redisDaoTmplData struct {
	Package       string
	Dao           RedisDaoDef
	DefaultPrefix string
	RedisName     string
	DefaultTTL    string
}

func redisDaoDefaultPrefix(dao RedisDaoDef) string {
	if dao.Prefix != "" {
		return dao.Prefix
	}
	return "cube:redisdao"
}

func redisDaoName(dao RedisDaoDef) string {
	if dao.RedisName != "" {
		return dao.RedisName
	}
	return toSnake(dao.Name)
}

func redisDaoDefaultTTL(dao RedisDaoDef) (string, error) {
	if dao.TTL == "" {
		return "0", nil
	}
	ttl, err := time.ParseDuration(dao.TTL)
	if err != nil {
		return "", fmt.Errorf("redis dao %s has invalid ttl %q: %w", dao.Name, dao.TTL, err)
	}
	return "time.Duration(" + strconv.FormatInt(int64(ttl), 10) + ")", nil
}
