package hyve

import (
	"fmt"
	"io"
	"log"
	// "net"
	"strings"
	"time"
	_ "unicode"
	"unicode/utf8"

	// "github.com/mitchellh/go-vnc"
	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/packer"
	"github.com/mitchellh/packer/template/interpolate"

	"github.com/huin/goserial"
)

//const KeyLeftShift uint32 = 0xFFE1

type bootCommandTemplateData struct {
	HTTPIP   string
	HTTPPort uint
	Name     string
}

// This step "types" the boot command into the VM.
//
// Uses:
//   config *config
//   http_port int
//   ui     packer.Ui
//
// Produces:
//   <nothing>
type stepTypeBootCommand struct {
	com1 io.ReadWriter
}

func (s *stepTypeBootCommand) Run(state multistep.StateBag) multistep.StepAction {
	config := state.Get("config").(*Config)
	// TODO get http_ip!!
	httpPort := state.Get("http_port").(uint)
	driver := state.Get("driver").(Driver)
	ui := state.Get("ui").(packer.Ui)

	tty := driver.TTY()
	ui.Say(fmt.Sprintf("Connecting to VM via serial port (COM1): %s", tty))

	ctx := config.ctx
	ctx.Data = &bootCommandTemplateData{
		// TODO get http_ip!!
		"10.0.2.2",
		httpPort,
		config.VMName,
	}

	comSettings := &goserial.Config{Name: tty, Baud: 9600}
	com1, err := goserial.OpenPort(comSettings)
	if err != nil {
		err := fmt.Errorf("Error connecting to %s: %s", tty, err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}
	s.com1 = com1

	ui.Say("Typing the boot command over serial...")
	for _, command := range config.BootCommand {
		command, err := interpolate.Render(command, &ctx)
		if err != nil {
			err := fmt.Errorf("Error preparing boot command: %s", err)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}

		// Check for interrupts between typing things so we can cancel
		// since this isn't the fastest thing.
		if _, ok := state.GetOk(multistep.StateCancelled); ok {
			return multistep.ActionHalt
		}

		ttySendString(com1, command)
	}
	return multistep.ActionContinue
}

func (*stepTypeBootCommand) Cleanup(multistep.StateBag) {}

func ttySendString(com1 io.ReadWriter, original string) {
	// Scancodes reference: https://github.com/qemu/qemu/blob/master/ui/vnc_keysym.h
	// special := make(map[string]uint32)
	// special["<bs>"] = 0xFF08
	// special["<del>"] = 0xFFFF
	// special["<enter>"] = 0xFF0D
	// special["<esc>"] = 0xFF1B
	// special["<f1>"] = 0xFFBE
	// special["<f2>"] = 0xFFBF
	// special["<f3>"] = 0xFFC0
	// special["<f4>"] = 0xFFC1
	// special["<f5>"] = 0xFFC2
	// special["<f6>"] = 0xFFC3
	// special["<f7>"] = 0xFFC4
	// special["<f8>"] = 0xFFC5
	// special["<f9>"] = 0xFFC6
	// special["<f10>"] = 0xFFC7
	// special["<f11>"] = 0xFFC8
	// special["<f12>"] = 0xFFC9
	// special["<return>"] = 0xFF0D
	// special["<tab>"] = 0xFF09
	// special["<up>"] = 0xFF52
	// special["<down>"] = 0xFF54
	// special["<left>"] = 0xFF51
	// special["<right>"] = 0xFF53
	// special["<spacebar>"] = 0x020
	// special["<insert>"] = 0xFF63
	// special["<home>"] = 0xFF50
	// special["<end>"] = 0xFF57
	// special["<pageUp>"] = 0xFF55
	// special["<pageDown>"] = 0xFF56

	// shiftedChars := "~!@#$%^&*()_+{}|:\"<>?"

	// TODO(mitchellh): Ripe for optimizations of some point, perhaps.
	for len(original) > 0 {
		var key byte
		//keyShift := false

		if strings.HasPrefix(original, "<wait>") {
			log.Printf("Special code '<wait>' found, sleeping one second")
			time.Sleep(1 * time.Second)
			original = original[len("<wait>"):]
			continue
		}

		if strings.HasPrefix(original, "<wait5>") {
			log.Printf("Special code '<wait5>' found, sleeping 5 seconds")
			time.Sleep(5 * time.Second)
			original = original[len("<wait5>"):]
			continue
		}

		if strings.HasPrefix(original, "<wait10>") {
			log.Printf("Special code '<wait10>' found, sleeping 10 seconds")
			time.Sleep(10 * time.Second)
			original = original[len("<wait10>"):]
			continue
		}

		// for specialCode, specialValue := range special {
		// 	if strings.HasPrefix(original, specialCode) {
		// 		log.Printf("Special code '%s' found, replacing with: %d", specialCode, specialValue)
		// 		keyCode = specialValue
		// 		original = original[len(specialCode):]
		// 		break
		// 	}
		// }

		if key == 0 {
			r, size := utf8.DecodeRuneInString(original)
			original = original[size:]
			key = byte(r)
			//keyShift = unicode.IsUpper(r) || strings.ContainsRune(shiftedChars, r)

			log.Printf("Sending char '%c', code %d", r, key)
		}

		//if keyShift {
		//	c.KeyEvent(KeyLeftShift, true)
		//}

		//c.KeyEvent(keyCode, true)
		ttySendKey(com1, key)
		//time.Sleep(time.Second / 10)
		//c.KeyEvent(keyCode, false)
		//time.Sleep(time.Second / 10)
		// TODO

		// if keyShift {
		// 	c.KeyEvent(KeyLeftShift, false)
		// }

		// qemu is picky, so no matter what, wait a small period
		time.Sleep(100 * time.Millisecond)
	}
}

func ttySendKey(com1 io.ReadWriter, key byte) error {

	// buf := new(bytes.Buffer)
	// err := binary.Write(buf, binary.LittleEndian, keyCode)
	// if err != nil {
	// 	fmt.Println("binary.Write failed:", err)
	// }

	// fmt.Printf("Encoded: % x\n", buf.Bytes())
	_, err := com1.Write([]byte{key})
	return err

	//for i := 0; i < 50; i++ {
	//	time.Sleep(100 * time.Millisecond)
	//	buf := make([]byte, 1024)
	//	_, err := s.Read(buf)
	//	if err != nil {
	//		fmt.Println(err)
	//	}

	//	fmt.Printf("%s", string(buf))
	//}

}
