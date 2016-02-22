package echo

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/webx-top/echo/engine"
	"github.com/webx-top/webx/lib/tagfast"
)

var DefaultHtmlFilter = func(v string) (r string) {
	return v
}

type (
	// Binder is the interface that wraps the Bind method.
	Binder interface {
		Bind(engine.Request, interface{}) error
	}
	binder struct {
		*Echo
	}
)

/*
func (binder) Bind(r engine.Request, i interface{}) (err error) {
	ct := r.Header().Get(ContentType)
	err = ErrUnsupportedMediaType
	if strings.HasPrefix(ct, ApplicationJSON) {
		if err = json.NewDecoder(r.Body()).Decode(i); err != nil {
			err = NewHTTPError(http.StatusBadRequest, err.Error())
		}
	} else if strings.HasPrefix(ct, ApplicationXML) {
		if err = xml.NewDecoder(r.Body()).Decode(i); err != nil {
			err = NewHTTPError(http.StatusBadRequest, err.Error())
		}
	}
	return
}
*/

func (b binder) Bind(r engine.Request, i interface{}) (err error) {
	body := r.Body()
	if body == nil {
		err = NewHTTPError(http.StatusBadRequest, "Request body can't be nil")
		return
	}
	defer body.Close()
	ct := r.Header().Get(ContentType)
	err = ErrUnsupportedMediaType
	if strings.HasPrefix(ct, ApplicationJSON) {
		err = json.NewDecoder(body).Decode(i)
	} else if strings.HasPrefix(ct, ApplicationXML) {
		err = xml.NewDecoder(body).Decode(i)
	} else if strings.HasPrefix(ct, ApplicationForm) {
		err = b.structMap(i, r.Form().All())
	} else if strings.Contains(ct, MultipartForm) {
		data := r.Form().All()
		if mf := r.MultipartForm(); mf != nil && mf.Value != nil {
			for key, vals := range mf.Value {
				data[key] = vals
			}
		}
		err = b.structMap(i, data)
	}
	return
}

// StructMap function mapping params to controller's properties
func (b binder) structMap(m interface{}, data map[string][]string) error {
	return NamedStructMap(b.Echo, m, data, "")
}

// user[name][test]
func SplitJson(s string) ([]string, error) {
	res := make([]string, 0)
	var begin, end int
	var isleft bool
	for i, r := range s {
		switch r {
		case '[':
			isleft = true
			if i > 0 && s[i-1] != ']' {
				if begin == end {
					return nil, errors.New("unknow character")
				}
				res = append(res, s[begin:end+1])
			}
			begin = i + 1
			end = begin
		case ']':
			if !isleft {
				return nil, errors.New("unknow character")
			}
			isleft = false
			if begin != end {
				//return nil, errors.New("unknow character")

				res = append(res, s[begin:end+1])
				begin = i + 1
				end = begin
			}
		default:
			end = i
		}
		if i == len(s)-1 && begin != end {
			res = append(res, s[begin:end+1])
		}
	}
	return res, nil
}

func NamedStructMap(e *Echo, m interface{}, data map[string][]string, topName string) error {
	vc := reflect.ValueOf(m)
	tc := reflect.TypeOf(m)

	switch tc.Kind() {
	case reflect.Struct:
	case reflect.Ptr:
		vc = vc.Elem()
		tc = tc.Elem()
	}

	for k, t := range data {

		if k == "" || k[0] == '_' {
			continue
		}

		if topName != "" {
			if !strings.HasPrefix(k, topName) {
				continue
			}
			k = k[len(topName)+1:]
		}

		v := t[0]
		names := strings.Split(k, ".")
		var err error
		length := len(names)
		if length == 1 {
			names, err = SplitJson(k)
			if err != nil {
				e.Logger().Warnf("Unrecognize form key %v %v", k, err)
				continue
			}
		}
		length = len(names)
		var value reflect.Value = vc
		var typev reflect.Type = tc
		for i, name := range names {
			name = strings.Title(name)

			//不是最后一个元素
			if i != length-1 {
				if value.Kind() != reflect.Struct {
					e.Logger().Warnf("arg error, value kind is %v", value.Kind())
					break
				}
				value = value.FieldByName(name)
				if !value.IsValid() {
					e.Logger().Warnf("(%v value is not valid %v)", name, value)
					break
				}
				if !value.CanSet() {
					e.Logger().Warnf("can not set %v -> %v", name, value.Interface())
					break
				}
				typev = value.Type()
				f, _ := typev.FieldByName(name)
				if tagfast.Value(tc, f, "form_options") == "-" {
					continue
				}
				if value.Kind() == reflect.Ptr {
					if value.IsNil() {
						value.Set(reflect.New(value.Type().Elem()))
					}
					value = value.Elem()
				}
			} else {
				if value.Kind() != reflect.Struct {
					e.Logger().Warnf("arg error, value %v kind is %v", name, value.Kind())
					break
				}
				tv := value.FieldByName(name)
				if !tv.IsValid() {
					break
				}
				if !tv.CanSet() {
					e.Logger().Warnf("can not set %v to %v", k, tv)
					break
				}
				f, _ := typev.FieldByName(name)
				if tagfast.Value(tc, f, "form_options") == "-" {
					continue
				}
				if tv.Kind() == reflect.Ptr {
					tv.Set(reflect.New(tv.Type().Elem()))
					tv = tv.Elem()
				}

				var l interface{}
				switch k := tv.Kind(); k {
				case reflect.String:
					switch tagfast.Value(tc, f, "form_filter") {
					case "html":
						v = DefaultHtmlFilter(v)
					}
					l = v
					tv.Set(reflect.ValueOf(l))
				case reflect.Bool:
					l = (v != "false" && v != "0")
					tv.Set(reflect.ValueOf(l))
				case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
					x, err := strconv.Atoi(v)
					if err != nil {
						e.Logger().Warnf("arg %v as int: %v", v, err)
						break
					}
					l = x
					tv.Set(reflect.ValueOf(l))
				case reflect.Int64:
					x, err := strconv.ParseInt(v, 10, 64)
					if err != nil {
						e.Logger().Warnf("arg %v as int64: %v", v, err)
						break
					}
					l = x
					tv.Set(reflect.ValueOf(l))
				case reflect.Float32, reflect.Float64:
					x, err := strconv.ParseFloat(v, 64)
					if err != nil {
						e.Logger().Warnf("arg %v as float64: %v", v, err)
						break
					}
					l = x
					tv.Set(reflect.ValueOf(l))
				case reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
					x, err := strconv.ParseUint(v, 10, 64)
					if err != nil {
						e.Logger().Warnf("arg %v as uint: %v", v, err)
						break
					}
					l = x
					tv.Set(reflect.ValueOf(l))
				case reflect.Struct:
					if tvf, ok := tv.Interface().(FromConversion); ok {
						err := tvf.FromString(v)
						if err != nil {
							e.Logger().Warnf("struct %v invoke FromString faild", tvf)
						}
					} else if tv.Type().String() == "time.Time" {
						x, err := time.Parse("2006-01-02 15:04:05.000 -0700", v)
						if err != nil {
							x, err = time.Parse("2006-01-02 15:04:05", v)
							if err != nil {
								x, err = time.Parse("2006-01-02", v)
								if err != nil {
									e.Logger().Warnf("unsupported time format %v, %v", v, err)
									break
								}
							}
						}
						l = x
						tv.Set(reflect.ValueOf(l))
					} else {
						e.Logger().Warn("can not set an struct which is not implement Fromconversion interface")
					}
				case reflect.Ptr:
					e.Logger().Warn("can not set an ptr of ptr")
				case reflect.Slice, reflect.Array:
					tt := tv.Type().Elem()
					tk := tt.Kind()

					if tv.IsNil() {
						tv.Set(reflect.MakeSlice(tv.Type(), len(t), len(t)))
					}

					for i, s := range t {
						var err error
						switch tk {
						case reflect.Int, reflect.Int16, reflect.Int32, reflect.Int8, reflect.Int64:
							var v int64
							v, err = strconv.ParseInt(s, 10, tt.Bits())
							if err == nil {
								tv.Index(i).SetInt(v)
							}
						case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
							var v uint64
							v, err = strconv.ParseUint(s, 10, tt.Bits())
							if err == nil {
								tv.Index(i).SetUint(v)
							}
						case reflect.Float32, reflect.Float64:
							var v float64
							v, err = strconv.ParseFloat(s, tt.Bits())
							if err == nil {
								tv.Index(i).SetFloat(v)
							}
						case reflect.Bool:
							var v bool
							v, err = strconv.ParseBool(s)
							if err == nil {
								tv.Index(i).SetBool(v)
							}
						case reflect.String:
							tv.Index(i).SetString(s)
						case reflect.Complex64, reflect.Complex128:
							// TODO:
							err = fmt.Errorf("unsupported slice element type %v", tk.String())
						default:
							err = fmt.Errorf("unsupported slice element type %v", tk.String())
						}
						if err != nil {
							e.Logger().Warnf("slice error: %v, %v", name, err)
							break
						}
					}
				default:
					break
				}
			}
		}
	}
	return nil
}

// a struct implements this interface can be convert from request param to a struct
type FromConversion interface {
	FromString(content string) error
}

// a struct implements this interface can be convert from struct to template variable
// Not Implemented
type ToConversion interface {
	ToString() string
}