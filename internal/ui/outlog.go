package ui

import "strings"

// maxOutLines bounds the output panel so a long pull cannot grow memory.
const maxOutLines = 2000

// outLog holds the output of the current or last operation. Compose repeats
// a progress line for each layer or container as its state changes; those
// updates replace the earlier line, so a pull reads as one line per layer.
type outLog struct {
	title string
	lines []string
	index map[string]int
}

func (o *outLog) reset(title string) {
	o.title = title
	o.lines = nil
	o.index = nil
}

func (o *outLog) empty() bool { return len(o.lines) == 0 }

func (o *outLog) add(line string) {
	line = strings.TrimRight(line, " ")
	if key := progressKey(line); key != "" {
		if i, ok := o.index[key]; ok && i < len(o.lines) {
			o.lines[i] = line
			return
		}
		if o.index == nil {
			o.index = map[string]int{}
		}
		o.index[key] = len(o.lines)
	}
	o.lines = append(o.lines, line)
	if n := len(o.lines) - maxOutLines; n > 0 {
		o.lines = append([]string(nil), o.lines[n:]...)
		for k, i := range o.index {
			if i < n {
				delete(o.index, k)
			} else {
				o.index[k] = i - n
			}
		}
	}
}

// progressKey names the object a compose progress line reports on, such as
// "Container app-web" or a 12-digit image layer ID. Other lines return "".
func progressKey(line string) string {
	f := strings.Fields(line)
	if len(f) < 2 {
		return ""
	}
	switch f[0] {
	case "Container", "Image", "Network", "Volume":
		return f[0] + " " + f[1]
	}
	if len(f[0]) == 12 && isHex(f[0]) {
		return f[0]
	}
	return ""
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
