package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"github.com/mcpjungle/mcpjungle/pkg/apierrors"
	"github.com/mcpjungle/mcpjungle/pkg/types"
)

func readUniqueJSON(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delim {
	case '{':
		obj := map[string]any{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("invalid object key")
			}
			if _, exists := obj[name]; exists {
				return nil, fmt.Errorf("duplicate JSON key %q", name)
			}
			value, err := readUniqueJSON(dec)
			if err != nil {
				return nil, err
			}
			obj[name] = value
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim('}') {
			return nil, fmt.Errorf("invalid object end")
		}
		return obj, nil
	case '[':
		values := []any{}
		for dec.More() {
			value, err := readUniqueJSON(dec)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		end, err := dec.Token()
		if err != nil || end != json.Delim(']') {
			return nil, fmt.Errorf("invalid array end")
		}
		return values, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter")
	}
}

// DecodeRegistrationJSON rejects ambiguous JSON before normalizing only the specified defaults.
func DecodeRegistrationJSON(data []byte, resolve bool) (*types.RegisterServerInput, string, error) {
	if err := validateRegistrationUnicode(data); err != nil {
		return nil, "", fmt.Errorf("invalid registration Unicode: %v: %w", err, apierrors.ErrInvalidInput)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := readUniqueJSON(dec)
	if err != nil {
		return nil, "", fmt.Errorf("invalid registration JSON: %v: %w", err, apierrors.ErrInvalidInput)
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, "", fmt.Errorf("trailing registration JSON: %w", apierrors.ErrInvalidInput)
	}
	if resolve {
		wrapper, ok := value.(map[string]any)
		if !ok || len(wrapper) != 1 {
			return nil, "", fmt.Errorf("resolve requires only registration: %w", apierrors.ErrInvalidInput)
		}
		value, ok = wrapper["registration"]
		if !ok {
			return nil, "", fmt.Errorf("registration is required: %w", apierrors.ErrInvalidInput)
		}
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("registration must be an object: %w", apierrors.ErrInvalidInput)
	}
	allowed := map[string]bool{"name": true, "transport": true, "command": true, "args": true, "env": true, "session_mode": true, "description": true}
	for k := range obj {
		if !allowed[k] {
			return nil, "", fmt.Errorf("unsupported registration field %q: %w", k, apierrors.ErrInvalidInput)
		}
	}
	input := &types.RegisterServerInput{Args: []string{}, Env: map[string]string{}}
	strings := map[string]*string{"name": &input.Name, "transport": &input.Transport, "command": &input.Command, "session_mode": &input.SessionMode, "description": &input.Description}
	for key, dest := range strings {
		value, exists := obj[key]
		if !exists {
			continue
		}
		s, ok := value.(string)
		if !ok {
			return nil, "", fmt.Errorf("%s must be a string: %w", key, apierrors.ErrInvalidInput)
		}
		*dest = s
	}
	if args, exists := obj["args"]; exists && args != nil {
		array, ok := args.([]any)
		if !ok {
			return nil, "", fmt.Errorf("args must be a string array: %w", apierrors.ErrInvalidInput)
		}
		for _, v := range array {
			s, ok := v.(string)
			if !ok {
				return nil, "", fmt.Errorf("args must contain strings: %w", apierrors.ErrInvalidInput)
			}
			input.Args = append(input.Args, s)
		}
	}
	if env, exists := obj["env"]; exists && env != nil {
		values, ok := env.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("env must be a string map: %w", apierrors.ErrInvalidInput)
		}
		for k, v := range values {
			s, ok := v.(string)
			if !ok {
				return nil, "", fmt.Errorf("env must contain strings: %w", apierrors.ErrInvalidInput)
			}
			input.Env[k] = s
		}
	}
	if input.SessionMode == "" {
		input.SessionMode = string(types.SessionModeStateless)
	}
	definition, err := NormalizeRegistration(input)
	return input, definition, err
}

func NormalizeRegistration(input *types.RegisterServerInput) (string, error) {
	if input == nil {
		return "", fmt.Errorf("registration is required: %w", apierrors.ErrInvalidInput)
	}
	if err := validateServerName(input.Name); err != nil {
		return "", err
	}
	values := []string{input.Name, input.Transport, input.Command, input.Description, input.SessionMode}
	values = append(values, input.Args...)
	for key, value := range input.Env {
		values = append(values, key, value)
	}
	for _, value := range values {
		if !utf8.ValidString(value) {
			return "", fmt.Errorf("invalid UTF-8 string: %w", apierrors.ErrInvalidInput)
		}
	}
	if input.Transport != string(types.TransportStdio) || input.Command == "" || !filepath.IsAbs(input.Command) {
		return "", fmt.Errorf("registration contract requires stdio and an absolute command: %w", apierrors.ErrInvalidInput)
	}
	if input.URL != "" || input.BearerToken != "" || len(input.Headers) != 0 || input.OAuthRedirectURI != "" || input.OAuthClientID != "" || input.OAuthClientSecret != "" || len(input.OAuthScopes) != 0 {
		return "", fmt.Errorf("unsupported registration fields: %w", apierrors.ErrInvalidInput)
	}
	mode, err := types.ValidateSessionMode(input.SessionMode)
	if err != nil {
		return "", fmt.Errorf("%v: %w", err, apierrors.ErrInvalidInput)
	}
	args := input.Args
	if args == nil {
		args = []string{}
	}
	env := input.Env
	if env == nil {
		env = map[string]string{}
	}
	canonical := struct {
		Name        string            `json:"name"`
		Transport   string            `json:"transport"`
		Command     string            `json:"command"`
		Args        []string          `json:"args"`
		Env         map[string]string `json:"env"`
		SessionMode string            `json:"session_mode"`
		Description string            `json:"description"`
	}{input.Name, input.Transport, input.Command, args, env, string(mode), input.Description}
	data, err := json.Marshal(canonical)
	return string(data), err
}

func validateRegistrationUnicode(data []byte) error {
	if !utf8.Valid(data) {
		return fmt.Errorf("invalid UTF-8")
	}
	quoted := false
	for i := 0; i < len(data); i++ {
		if data[i] == '"' {
			quoted = !quoted
			continue
		}
		if !quoted || data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return fmt.Errorf("invalid escape")
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return fmt.Errorf("invalid Unicode escape")
		}
		unit, err := strconv.ParseUint(string(data[i+1:i+5]), 16, 16)
		if err != nil {
			return err
		}
		i += 4
		if unit >= 0xdc00 && unit <= 0xdfff {
			return fmt.Errorf("unpaired low surrogate")
		}
		if unit < 0xd800 || unit > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return fmt.Errorf("unpaired high surrogate")
		}
		low, err := strconv.ParseUint(string(data[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("invalid surrogate pair")
		}
		i += 6
	}
	return nil
}
