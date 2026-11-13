package controls

// Command represents a user input command
type Command struct {
	Type  CommandType
	Value any
}

// CommandType represents the type of command
type CommandType int

const (
	CmdQuit CommandType = iota
	CmdToggleColor
	CmdIncreaseQuality
	CmdDecreaseQuality
	CmdSwitchRenderer
	CmdToggleSize
	CmdShowHelp
)

const keyEsc = 0x1b

// keyParser turns raw terminal input into commands. An escape sequence can be
// split across reads, so an incomplete one is kept until more input arrives
// or the read times out (flush).
type keyParser struct {
	pending []byte
}

// feed parses data and returns the commands it contains.
func (p *keyParser) feed(data []byte) []Command {
	p.pending = append(p.pending, data...)
	buf := p.pending
	p.pending = nil

	var cmds []Command
	for i := 0; i < len(buf); {
		if buf[i] != keyEsc {
			if cmd := keyCommand(buf[i]); cmd != nil {
				cmds = append(cmds, *cmd)
			}
			i++
			continue
		}

		n, complete := escapeLen(buf[i:])
		if !complete {
			p.pending = append([]byte(nil), buf[i:]...)
			break
		}
		if n == 1 {
			cmds = append(cmds, Command{Type: CmdQuit}) // lone Esc
		}
		i += n
	}
	return cmds
}

// flush is called when a read times out. A pending lone Esc is a key press;
// any other incomplete sequence is dropped.
func (p *keyParser) flush() []Command {
	pending := p.pending
	p.pending = nil
	if len(pending) == 1 && pending[0] == keyEsc {
		return []Command{{Type: CmdQuit}}
	}
	return nil
}

// escapeLen returns the length of the escape sequence at the start of b
// (b[0] == Esc) and whether it is complete. A length of 1 means a lone Esc.
// Sequences are consumed without producing commands: CSI (Esc [ … final byte),
// SS3 (Esc O x, e.g. F1–F4), and Alt+key (Esc x).
func escapeLen(b []byte) (n int, complete bool) {
	if len(b) == 1 {
		return 1, false
	}
	switch b[1] {
	case keyEsc:
		return 1, true
	case '[':
		for j := 2; j < len(b); j++ {
			c := b[j]
			switch {
			case c >= 0x40 && c <= 0x7e: // final byte
				return j + 1, true
			case c >= 0x20 && c <= 0x3f: // parameter or intermediate byte
			default: // malformed: drop what we have
				return j, true
			}
		}
		return len(b), false
	case 'O':
		if len(b) < 3 {
			return len(b), false
		}
		return 3, true
	default:
		return 2, true
	}
}

// keyCommand maps a single key byte to a command, or nil if it has none.
func keyCommand(b byte) *Command {
	switch b {
	case 'q', 'Q':
		return &Command{Type: CmdQuit}
	case 'c', 'C':
		return &Command{Type: CmdToggleColor}
	case '+', '=':
		return &Command{Type: CmdIncreaseQuality}
	case '-', '_':
		return &Command{Type: CmdDecreaseQuality}
	case 'f', 'F':
		return &Command{Type: CmdToggleSize}
	case 'h', 'H', '?':
		return &Command{Type: CmdShowHelp}
	case '1', '2', '3', '4', '5', '6', '7':
		rendererIndex := int(b - '1')
		return &Command{Type: CmdSwitchRenderer, Value: rendererIndex}
	}
	return nil
}
