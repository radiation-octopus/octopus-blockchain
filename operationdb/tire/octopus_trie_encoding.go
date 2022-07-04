package tire

// Trie密钥由三种不同的编码处理：KEYBYTES编码包含实际密钥，而不包含其他内容。这种编码是大多数API函数的输入。
//十六进制编码包含键的每个半字节一个字节和值0x10的可选尾部“终止符”字节，该字节指示键处的节点是否包含值。
//十六进制编码用于加载到内存中的节点，因为它便于访问。
//紧凑编码由以太坊黄皮定义（在那里称为“十六进制前缀编码”），包含密钥字节和标志。
//第一个字节的高位半字节包含标志；最低位编码长度的奇数，第二低位编码键处的节点是否为值节点。
//对于偶数个半字节，第一个字节的低半字节为零；对于奇数个半字节，第一个半字节为零。
//所有剩余的半字节（现在是偶数）正确地放入剩余的字节中。压缩编码用于存储在磁盘上的节点。
func hexToCompact(hex []byte) []byte {
	terminator := byte(0)
	if hasTerm(hex) {
		terminator = 1
		hex = hex[:len(hex)-1]
	}
	buf := make([]byte, len(hex)/2+1)
	buf[0] = terminator << 5 // 标志字节
	if len(hex)&1 == 1 {
		buf[0] |= 1 << 4 // 奇数标志
		buf[0] |= hex[0] // 第一个半字节包含在第一个字节中
		hex = hex[1:]
	}
	decodeNibbles(hex, buf[1:])
	return buf
}

func decodeNibbles(nibbles []byte, bytes []byte) {
	for bi, ni := 0, 0; ni < len(nibbles); bi, ni = bi+1, ni+2 {
		bytes[bi] = nibbles[ni]<<4 | nibbles[ni+1]
	}
}

func compactToHex(compact []byte) []byte {
	if len(compact) == 0 {
		return compact
	}
	base := keybytesToHex(compact)
	// 删除终止符标志
	if base[0] < 2 {
		base = base[:len(base)-1]
	}
	// 应用奇数标志
	chop := 2 - base[0]&1
	return base[chop:]
}

func keybytesToHex(str []byte) []byte {
	l := len(str)*2 + 1
	var nibbles = make([]byte, l)
	for i, b := range str {
		nibbles[i*2] = b / 16
		nibbles[i*2+1] = b % 16
	}
	nibbles[l-1] = 16
	return nibbles
}

//prefixLen返回a和b的公共前缀的长度。
func prefixLen(a, b []byte) int {
	var i, length = 0, len(a)
	if len(b) < length {
		length = len(b)
	}
	for ; i < length; i++ {
		if a[i] != b[i] {
			break
		}
	}
	return i
}

// hasTerm返回十六进制键是否具有终止符标志。
func hasTerm(s []byte) bool {
	return len(s) > 0 && s[len(s)-1] == 16
}
