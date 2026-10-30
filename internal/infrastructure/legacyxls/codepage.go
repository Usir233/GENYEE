package legacyxls

import (
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
)

// textDecoder decodes BIFF5 byte strings, which are stored in the code page
// named by the workbook's CODEPAGE record. BIFF8 strings are always Unicode
// and do not go through it.
type textDecoder struct {
	enc encoding.Encoding // nil means the bytes are already UTF-8 or ASCII
}

// codePages maps CODEPAGE record values ([MS-XLS] 2.4.52) to encodings.
var codePages = map[uint16]encoding.Encoding{
	437:   charmap.CodePage437,
	850:   charmap.CodePage850,
	852:   charmap.CodePage852,
	855:   charmap.CodePage855,
	858:   charmap.CodePage858,
	860:   charmap.CodePage860,
	862:   charmap.CodePage862,
	863:   charmap.CodePage863,
	865:   charmap.CodePage865,
	866:   charmap.CodePage866,
	874:   charmap.Windows874,
	932:   japanese.ShiftJIS,
	936:   simplifiedchinese.GBK,
	949:   korean.EUCKR,
	950:   traditionalchinese.Big5,
	1200:  unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM),
	1250:  charmap.Windows1250,
	1251:  charmap.Windows1251,
	1252:  charmap.Windows1252,
	1253:  charmap.Windows1253,
	1254:  charmap.Windows1254,
	1255:  charmap.Windows1255,
	1256:  charmap.Windows1256,
	1257:  charmap.Windows1257,
	1258:  charmap.Windows1258,
	10000: charmap.Macintosh,
	10007: charmap.MacintoshCyrillic,
	20866: charmap.KOI8R,
	21866: charmap.KOI8U,
	28591: charmap.ISO8859_1,
	28592: charmap.ISO8859_2,
	32768: charmap.Macintosh, // Apple Roman as written by Excel for Mac
	32769: charmap.Windows1252,
	54936: simplifiedchinese.GB18030,
	65001: nil, // UTF-8
}

func newTextDecoder(codePage uint16) *textDecoder {
	if codePage == 367 { // US-ASCII
		return &textDecoder{enc: charmap.Windows1252}
	}
	enc, ok := codePages[codePage]
	if !ok {
		enc = charmap.Windows1252
	}
	return &textDecoder{enc: enc}
}

func (d *textDecoder) decode(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	ascii := true
	for _, c := range b {
		if c >= 0x80 {
			ascii = false
			break
		}
	}
	if ascii && d.enc != codePages[1200] {
		return string(b)
	}
	if d.enc == nil {
		return string(b)
	}
	out, err := d.enc.NewDecoder().Bytes(b)
	if err != nil || !utf8.Valid(out) {
		return latin1(b)
	}
	return string(out)
}
