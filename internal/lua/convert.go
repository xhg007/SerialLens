package lua

import (
	"fmt"
	"sort"

	glua "github.com/yuin/gopher-lua"
)

// maxConvertDepth 防止脚本构造自引用表导致无限递归。
const maxConvertDepth = 8

// tableToMap 把 on_frame 返回的字段表转成 Go map；纯数组表转成 []any。
func tableToMap(tbl *glua.LTable) map[string]any {
	out := map[string]any{}
	tbl.ForEach(func(k, v glua.LValue) {
		out[luaKeyToString(k)] = luaValueToAny(v, 1)
	})
	return out
}

func luaKeyToString(k glua.LValue) string {
	if s, ok := k.(glua.LString); ok {
		return string(s)
	}
	return k.String()
}

func luaValueToAny(v glua.LValue, depth int) any {
	if depth > maxConvertDepth {
		return nil
	}
	switch val := v.(type) {
	case *glua.LTable:
		if isArray(val) {
			arr := make([]any, 0, val.Len())
			for i := 1; i <= val.Len(); i++ {
				arr = append(arr, luaValueToAny(val.RawGetInt(i), depth+1))
			}
			return arr
		}
		m := map[string]any{}
		val.ForEach(func(k, vv glua.LValue) {
			m[luaKeyToString(k)] = luaValueToAny(vv, depth+1)
		})
		return m
	case glua.LNumber:
		return float64(val)
	case glua.LString:
		return string(val)
	case glua.LBool:
		return bool(val)
	default:
		return nil
	}
}

// isArray 判断表是否为 1..n 的稠密数组。
func isArray(tbl *glua.LTable) bool {
	n := tbl.Len()
	if n == 0 {
		return false
	}
	count := 0
	isArr := true
	tbl.ForEach(func(k, _ glua.LValue) {
		count++
		num, ok := k.(glua.LNumber)
		if !ok || float64(num) != float64(int(num)) || int(num) < 1 || int(num) > n {
			isArr = false
		}
	})
	return isArr && count == n
}

// mapToTable params map → Lua table（嵌套 map/切片递归转换）。
func mapToTable(L *glua.LState, params map[string]any) *glua.LTable {
	tbl := L.NewTable()
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		tbl.RawSetString(k, anyToLuaValue(L, params[k], 1))
	}
	return tbl
}

func anyToLuaValue(L *glua.LState, v any, depth int) glua.LValue {
	if depth > maxConvertDepth {
		return glua.LNil
	}
	switch val := v.(type) {
	case nil:
		return glua.LNil
	case bool:
		return glua.LBool(val)
	case string:
		return glua.LString(val)
	case int:
		return glua.LNumber(val)
	case int64:
		return glua.LNumber(val)
	case uint64:
		return glua.LNumber(val)
	case float64:
		return glua.LNumber(val)
	case []byte:
		return glua.LString(string(val))
	case []any:
		tbl := L.NewTable()
		for i, item := range val {
			tbl.RawSetInt(i+1, anyToLuaValue(L, item, depth+1))
		}
		return tbl
	case map[string]any:
		tbl := L.NewTable()
		for k, item := range val {
			tbl.RawSetString(k, anyToLuaValue(L, item, depth+1))
		}
		return tbl
	default:
		return glua.LString(fmt.Sprint(v))
	}
}

// luaValueToBytes 接受 string，或 1..n 的数字数组（0..255）。
func luaValueToBytes(v glua.LValue) ([]byte, bool) {
	switch val := v.(type) {
	case glua.LString:
		return []byte(string(val)), true
	case *glua.LTable:
		n := val.Len()
		out := make([]byte, 0, n)
		for i := 1; i <= n; i++ {
			num, ok := val.RawGetInt(i).(glua.LNumber)
			if !ok {
				return nil, false
			}
			out = append(out, byte(int(num)&0xFF))
		}
		return out, true
	default:
		return nil, false
	}
}
