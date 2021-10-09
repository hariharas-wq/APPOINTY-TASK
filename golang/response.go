package jsonapi

import (
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var (
	ErrBadJSONAPIStructTag = errors.New("Bad jsonapi struct tag format")

	ErrBadJSONAPIID = errors.New(
		"id should be either string, int(8,16,32,64) or uint(8,16,32,64)")

	ErrExpectedSlice = errors.New("models should be a slice of struct pointers")

	ErrUnexpectedType = errors.New("models should be a struct pointer or slice of struct pointers")
)

func MarshalPayload(w io.Writer, models interface{}) error {
	payload, err := Marshal(models)
	if err != nil {
		return err
	}

	return json.NewEncoder(w).Encode(payload)
}

func Marshal(models interface{}) (Payloader, error) {
	switch vals := reflect.ValueOf(models); vals.Kind() {
	case reflect.Slice:
		m, err := convertToSliceInterface(&models)
		if err != nil {
			return nil, err
		}

		payload, err := marshalMany(m)
		if err != nil {
			return nil, err
		}

		if linkableModels, isLinkable := models.(Linkable); isLinkable {
			jl := linkableModels.JSONAPILinks()
			if er := jl.validate(); er != nil {
				return nil, er
			}
			payload.Links = linkableModels.JSONAPILinks()
		}

		if metableModels, ok := models.(Metable); ok {
			payload.Meta = metableModels.JSONAPIMeta()
		}

		return payload, nil
	case reflect.Ptr:

		if reflect.Indirect(vals).Kind() != reflect.Struct {
			return nil, ErrUnexpectedType
		}
		return marshalOne(models)
	default:
		return nil, ErrUnexpectedType
	}
}

func MarshalOnePayloadEmbedded(w io.Writer, model interface{}) error {
	rootNode, err := visitModelNode(model, nil, false)
	if err != nil {
		return err
	}

	payload := &OnePayload{Data: rootNode}

	return json.NewEncoder(w).Encode(payload)
}

func visitModelNode(model interface{}, included *map[string]*Node,
	sideload bool) (*Node, error) {
	node := new(Node)

	var er error
	value := reflect.ValueOf(model)
	if value.IsNil() {
		return nil, nil
	}

	modelValue := value.Elem()
	modelType := value.Type().Elem()

	for i := 0; i < modelValue.NumField(); i++ {
		structField := modelValue.Type().Field(i)
		tag := structField.Tag.Get(annotationJSONAPI)
		if tag == "" {
			continue
		}

		fieldValue := modelValue.Field(i)
		fieldType := modelType.Field(i)

		args := strings.Split(tag, annotationSeperator)

		if len(args) < 1 {
			er = ErrBadJSONAPIStructTag
			break
		}

		annotation := args[0]

		if (annotation == annotationClientID && len(args) != 1) ||
			(annotation != annotationClientID && len(args) < 2) {
			er = ErrBadJSONAPIStructTag
			break
		}

		if annotation == annotationPrimary {
			v := fieldValue

			var kind reflect.Kind
			if fieldValue.Kind() == reflect.Ptr {
				kind = fieldType.Type.Elem().Kind()
				v = reflect.Indirect(fieldValue)
			} else {
				kind = fieldType.Type.Kind()
			}

			switch kind {
			case reflect.String:
				node.ID = v.Interface().(string)
			case reflect.Int:
				node.ID = strconv.FormatInt(int64(v.Interface().(int)), 10)
			case reflect.Int8:
				node.ID = strconv.FormatInt(int64(v.Interface().(int8)), 10)
			case reflect.Int16:
				node.ID = strconv.FormatInt(int64(v.Interface().(int16)), 10)
			case reflect.Int32:
				node.ID = strconv.FormatInt(int64(v.Interface().(int32)), 10)
			case reflect.Int64:
				node.ID = strconv.FormatInt(v.Interface().(int64), 10)
			case reflect.Uint:
				node.ID = strconv.FormatUint(uint64(v.Interface().(uint)), 10)
			case reflect.Uint8:
				node.ID = strconv.FormatUint(uint64(v.Interface().(uint8)), 10)
			case reflect.Uint16:
				node.ID = strconv.FormatUint(uint64(v.Interface().(uint16)), 10)
			case reflect.Uint32:
				node.ID = strconv.FormatUint(uint64(v.Interface().(uint32)), 10)
			case reflect.Uint64:
				node.ID = strconv.FormatUint(v.Interface().(uint64), 10)
			default:

				er = ErrBadJSONAPIID
			}

			if er != nil {
				break
			}

			node.Type = args[1]
		} else if annotation == annotationClientID {
			clientID := fieldValue.String()
			if clientID != "" {
				node.ClientID = clientID
			}
		} else if annotation == annotationAttribute {
			var omitEmpty, iso8601, rfc3339 bool

			if len(args) > 2 {
				for _, arg := range args[2:] {
					switch arg {
					case annotationOmitEmpty:
						omitEmpty = true
					case annotationISO8601:
						iso8601 = true
					case annotationRFC3339:
						rfc3339 = true
					}
				}
			}

			if node.Attributes == nil {
				node.Attributes = make(map[string]interface{})
			}

			if fieldValue.Type() == reflect.TypeOf(time.Time{}) {
				t := fieldValue.Interface().(time.Time)

				if t.IsZero() {
					continue
				}

				if iso8601 {
					node.Attributes[args[1]] = t.UTC().Format(iso8601TimeFormat)
				} else if rfc3339 {
					node.Attributes[args[1]] = t.UTC().Format(time.RFC3339)
				} else {
					node.Attributes[args[1]] = t.Unix()
				}
			} else if fieldValue.Type() == reflect.TypeOf(new(time.Time)) {
				// A time pointer may be nil
				if fieldValue.IsNil() {
					if omitEmpty {
						continue
					}

					node.Attributes[args[1]] = nil
				} else {
					tm := fieldValue.Interface().(*time.Time)

					if tm.IsZero() && omitEmpty {
						continue
					}

					if iso8601 {
						node.Attributes[args[1]] = tm.UTC().Format(iso8601TimeFormat)
					} else if rfc3339 {
						node.Attributes[args[1]] = tm.UTC().Format(time.RFC3339)
					} else {
						node.Attributes[args[1]] = tm.Unix()
					}
				}
			} else {
				// Dealing with a fieldValue that is not a time
				emptyValue := reflect.Zero(fieldValue.Type())

				// See if we need to omit this field
				if omitEmpty && reflect.DeepEqual(fieldValue.Interface(), emptyValue.Interface()) {
					continue
				}

				strAttr, ok := fieldValue.Interface().(string)
				if ok {
					node.Attributes[args[1]] = strAttr
				} else {
					node.Attributes[args[1]] = fieldValue.Interface()
				}
			}
		} else if annotation == annotationRelation {
			var omitEmpty bool

			//add support for 'omitempty' struct tag for marshaling as absent
			if len(args) > 2 {
				omitEmpty = args[2] == annotationOmitEmpty
			}

			isSlice := fieldValue.Type().Kind() == reflect.Slice
			if omitEmpty &&
				(isSlice && fieldValue.Len() < 1 ||
					(!isSlice && fieldValue.IsNil())) {
				continue
			}

			if node.Relationships == nil {
				node.Relationships = make(map[string]interface{})
			}

			var relLinks *Links
			if linkableModel, ok := model.(RelationshipLinkable); ok {
				relLinks = linkableModel.JSONAPIRelationshipLinks(args[1])
			}

			var relMeta *Meta
			if metableModel, ok := model.(RelationshipMetable); ok {
				relMeta = metableModel.JSONAPIRelationshipMeta(args[1])
			}

			if isSlice {
				// to-many relationship
				relationship, err := visitModelNodeRelationships(
					fieldValue,
					included,
					sideload,
				)
				if err != nil {
					er = err
					break
				}
				relationship.Links = relLinks
				relationship.Meta = relMeta

				if sideload {
					shallowNodes := []*Node{}
					for _, n := range relationship.Data {
						appendIncluded(included, n)
						shallowNodes = append(shallowNodes, toShallowNode(n))
					}

					node.Relationships[args[1]] = &RelationshipManyNode{
						Data:  shallowNodes,
						Links: relationship.Links,
						Meta:  relationship.Meta,
					}
				} else {
					node.Relationships[args[1]] = relationship
				}
			} else {
				// to-one relationships

				// Handle null relationship case
				if fieldValue.IsNil() {
					node.Relationships[args[1]] = &RelationshipOneNode{Data: nil}
					continue
				}

				relationship, err := visitModelNode(
					fieldValue.Interface(),
					included,
					sideload,
				)
				if err != nil {
					er = err
					break
				}

				if sideload {
					appendIncluded(included, relationship)
					node.Relationships[args[1]] = &RelationshipOneNode{
						Data:  toShallowNode(relationship),
						Links: relLinks,
						Meta:  relMeta,
					}
				} else {
					node.Relationships[args[1]] = &RelationshipOneNode{
						Data:  relationship,
						Links: relLinks,
						Meta:  relMeta,
					}
				}
			}

		} else {
			er = ErrBadJSONAPIStructTag
			break
		}
	}

	if er != nil {
		return nil, er
	}

	if linkableModel, isLinkable := model.(Linkable); isLinkable {
		jl := linkableModel.JSONAPILinks()
		if er := jl.validate(); er != nil {
			return nil, er
		}
		node.Links = linkableModel.JSONAPILinks()
	}

	if metableModel, ok := model.(Metable); ok {
		node.Meta = metableModel.JSONAPIMeta()
	}

	return node, nil
}
