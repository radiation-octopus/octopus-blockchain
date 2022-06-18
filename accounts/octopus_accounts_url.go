package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

//URL表示钱包或帐户的规范标识URL。这是url的简化版本。
//URL，具有重要的限制（此处认为是功能），即它仅包含值可复制组件，并且不进行任何特殊字符的URL编码/解码。
//前者对于允许复制帐户而不保留对原始版本的实时引用很重要，而后者对于确保一个单一的规范形式而不是RFC 3986规范允许的多个规范形式很重要。
//因此，这些URL不应在octopus钱包或帐户的范围之外使用。
type URL struct {
	Scheme string // 用于标识可用帐户后端的协议方案
	Path   string // 后端用于标识唯一实体的路径
}

// parseURL将用户提供的URL转换为特定于帐户的结构。
func parseURL(url string) (URL, error) {
	parts := strings.Split(url, "://")
	if len(parts) != 2 || parts[0] == "" {
		return URL{}, errors.New("protocol scheme missing")
	}
	return URL{
		Scheme: parts[0],
		Path:   parts[1],
	}, nil
}

// String实现stringer接口。
func (u URL) String() string {
	if u.Scheme != "" {
		return fmt.Sprintf("%s://%s", u.Scheme, u.Path)
	}
	return u.Path
}

// TerminalString实现日志。TerminalStringer接口。
func (u URL) TerminalString() string {
	url := u.String()
	if len(url) > 32 {
		return url[:31] + ".."
	}
	return url
}

// MarshalJSON实现json。Marshaller接口。
func (u URL) MarshalJSON() ([]byte, error) {
	return json.Marshal(u.String())
}

// Unmarshalson解析url。
func (u *URL) UnmarshalJSON(input []byte) error {
	var textURL string
	err := json.Unmarshal(input, &textURL)
	if err != nil {
		return err
	}
	url, err := parseURL(textURL)
	if err != nil {
		return err
	}
	u.Scheme = url.Scheme
	u.Path = url.Path
	return nil
}

// Cmp比较x和y并返回：
//
//   -1 if x <  y
//    0 if x == y
//   +1 if x >  y
//
func (u URL) Cmp(url URL) int {
	if u.Scheme == url.Scheme {
		return strings.Compare(u.Path, url.Path)
	}
	return strings.Compare(u.Scheme, url.Scheme)
}
