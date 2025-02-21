package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/melt"
	"github.com/mattn/go-isatty"
	"github.com/mattn/go-tty"
	mcobra "github.com/muesli/mango-cobra"
	"github.com/muesli/reflow/wordwrap"
	"github.com/muesli/roff"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
	"github.com/tyler-smith/go-bip39"
	"github.com/tyler-smith/go-bip39/wordlists"
	"golang.org/x/crypto/ssh"
	"golang.org/x/term"
	lang "golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

const (
	maxWidth = 72
)

var (
	baseStyle = lipgloss.NewStyle().Margin(0, 0, 1, 2) //nolint: gomnd
	violet    = lipgloss.Color(completeColor("#6B50FF", "63", "12"))
	cmdStyle  = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#FF5E8E", Dark: "#FF5E8E"}).
			Background(lipgloss.AdaptiveColor{Light: completeColor("#ECECEC", "255", "7"), Dark: "#1F1F1F"}).
			Padding(0, 1)
	mnemonicStyle = baseStyle.Copy().
			Foreground(violet).
			Background(lipgloss.AdaptiveColor{Light: completeColor("#EEEBFF", "255", "7"), Dark: completeColor("#1B1731", "235", "8")}).
			Padding(1, 2) //nolint: gomnd
	keyPathStyle = lipgloss.NewStyle().Foreground(violet)

	mnemonic string
	language string

	rootCmd = &cobra.Command{
		Use: "melt",
		Example: `  melt ~/.ssh/id_ed25519
  melt ~/.ssh/id_ed25519 > seed
  melt restore --seed "seed phrase" ./restored_id25519
  melt restore ./restored_id25519 < seed`,
		Short: "Generate a seed phrase from an SSH key",
		Long: `melt generates a seed phrase from an SSH key. That phrase can
be used to rebuild your public and private keys.`,
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := setLanguage(language); err != nil {
				return err
			}

			var keyPath string
			if len(args) > 0 {
				keyPath = args[0]
			}

			mnemonic, err := backup(keyPath, nil)
			if err != nil {
				return err
			}
			if isatty.IsTerminal(os.Stdout.Fd()) {
				b := strings.Builder{}
				w := getWidth(maxWidth)

				b.WriteRune('\n')
				meltCmd := cmdStyle.Render(os.Args[0])
				renderBlock(&b, baseStyle, w, fmt.Sprintf("OK! Your key has been melted down to the seed phrase below. Store it somewhere safe. You can use %s to recover your key at any time.", meltCmd))
				renderBlock(&b, mnemonicStyle, w, mnemonic)
				renderBlock(&b, baseStyle, w, "To recreate this key run:")

				// Build formatted restore command
				const cmdEOL = " \\"
				var lang string
				if language != "en" {
					lang = fmt.Sprintf(" --language %s", language)
				}
				cmd := wordwrap.String(
					os.Args[0]+` restore`+lang+` ./my-key --seed "`+mnemonic+`"`,
					w-lipgloss.Width(cmdEOL)-baseStyle.GetHorizontalFrameSize()*2,
				)
				leftPad := strings.Repeat(" ", baseStyle.GetMarginLeft())
				cmdLines := strings.Split(cmd, "\n")
				for i, l := range cmdLines {
					b.WriteString(leftPad)
					b.WriteString(l)
					if i < len(cmdLines)-1 {
						b.WriteString(cmdEOL)
						b.WriteRune('\n')
					}
				}
				b.WriteRune('\n')

				fmt.Println(b.String())
			} else {
				fmt.Print(mnemonic)
			}
			return nil
		},
	}

	restoreCmd = &cobra.Command{
		Use:   "restore",
		Short: "Recreate a key using the given seed phrase",
		Example: `  melt restore --seed "seed phrase" ./restored_id25519
  melt restore ./restored_id25519 < seed`,
		Aliases: []string{"res", "r"},
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := setLanguage(language); err != nil {
				return err
			}

			switch args[0] {
			case "-":
				_, _ = fmt.Fprint(os.Stderr, "Restoring key to STDOUT...\n")
				return restore(maybeFile(mnemonic), askNewPassphrase, restoreToWriter(cmd.OutOrStdout()))
			default:
				name := args[0]
				_, _ = fmt.Fprintf(os.Stderr, "Restoring key to %s and %[1]s.pub...\n", name)
				if err := restore(maybeFile(mnemonic), askNewPassphrase, restoreToFiles(name)); err != nil {
					return err
				}

				pub := keyPathStyle.Render(name)
				priv := keyPathStyle.Render(name + ".pub")
				fmt.Println(baseStyle.Render(fmt.Sprintf("\nSuccessfully restored keys to %s and %s", pub, priv)))
			}
			return nil
		},
	}

	manCmd = &cobra.Command{
		Use:          "man",
		Args:         cobra.NoArgs,
		Short:        "generate man pages",
		Hidden:       true,
		SilenceUsage: true,
		RunE: func(*cobra.Command, []string) error {
			manPage, err := mcobra.NewManPage(1, rootCmd)
			if err != nil {
				//nolint: wrapcheck
				return err
			}
			manPage = manPage.WithSection("Copyright", "(C) 2022 Charmbracelet, Inc.\n"+
				"Released under MIT license.")
			fmt.Println(manPage.Build(roff.NewDocument()))
			return nil
		},
	}
)

func init() {
	rootCmd.PersistentFlags().StringVarP(&language, "language", "l", "en", "Language")
	rootCmd.AddCommand(restoreCmd, manCmd)

	restoreCmd.PersistentFlags().StringVarP(&mnemonic, "seed", "s", "-", "Seed phrase")
	_ = restoreCmd.MarkFlagRequired("seed")
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func maybeFile(s string) string {
	f, err := openFileOrStdin(s)
	if err != nil {
		return s
	}
	defer f.Close() //nolint:errcheck
	bts, err := io.ReadAll(f)
	if err != nil {
		return s
	}
	return string(bts)
}

func openFileOrStdin(path string) (*os.File, error) {
	if path == "-" {
		return os.Stdin, nil
	}

	if fi, _ := os.Stdin.Stat(); (fi.Mode() & os.ModeNamedPipe) != 0 {
		return os.Stdin, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open %s: %w", path, err)
	}
	return f, nil
}

func parsePrivateKey(bts, pass []byte) (interface{}, error) {
	if len(pass) == 0 {
		//nolint: wrapcheck
		return ssh.ParseRawPrivateKey(bts)
	}
	//nolint: wrapcheck
	return ssh.ParseRawPrivateKeyWithPassphrase(bts, pass)
}

func backup(path string, pass []byte) (string, error) {
	f, err := openFileOrStdin(path)
	if err != nil {
		return "", fmt.Errorf("could not read key: %w", err)
	}
	defer f.Close() //nolint:errcheck
	bts, err := io.ReadAll(f)
	if err != nil {
		return "", fmt.Errorf("could not read key: %w", err)
	}

	key, err := parsePrivateKey(bts, pass)
	if err != nil && isPasswordError(err) {
		pass, err := askKeyPassphrase(path)
		if err != nil {
			return "", err
		}
		return backup(path, pass)
	}
	if err != nil {
		return "", fmt.Errorf("could not parse key: %w", err)
	}

	switch key := key.(type) {
	case *ed25519.PrivateKey:
		//nolint: wrapcheck
		return melt.ToMnemonic(key)
	default:
		return "", fmt.Errorf("unknown key type: %v", key)
	}
}

func isPasswordError(err error) bool {
	var kerr *ssh.PassphraseMissingError
	return errors.As(err, &kerr)
}

func marshallPrivateKey(key ed25519.PrivateKey, pass []byte) (*pem.Block, error) {
	if len(pass) == 0 {
		//nolint: wrapcheck
		return ssh.MarshalPrivateKey(key, "")
	}
	//nolint: wrapcheck
	return ssh.MarshalPrivateKeyWithPassphrase(key, "", pass)
}

func restore(mnemonic string, passFn func() ([]byte, error), outFn func(pem, pub []byte) error) error {
	pvtKey, err := melt.FromMnemonic(mnemonic)
	if err != nil {
		//nolint: wrapcheck
		return err
	}

	pass, err := passFn()
	if err != nil {
		return err
	}

	block, err := marshallPrivateKey(pvtKey, pass)
	if err != nil {
		return fmt.Errorf("could not marshal private key: %w", err)
	}

	pubkey, err := ssh.NewPublicKey(pvtKey.Public())
	if err != nil {
		return fmt.Errorf("could not prepare public key: %w", err)
	}

	return outFn(pem.EncodeToMemory(block), ssh.MarshalAuthorizedKey(pubkey))
}

func restoreToWriter(w io.Writer) func(pem, _ []byte) error {
	return func(pem, _ []byte) error {
		if _, err := fmt.Fprint(w, string(pem)); err != nil {
			return fmt.Errorf("could not write private key: %w", err)
		}
		return nil
	}
}

func restoreToFiles(path string) func(pem, pub []byte) error {
	return func(pem, pub []byte) error {
		if err := os.WriteFile(path, pem, 0o600); err != nil { //nolint: gomnd
			return fmt.Errorf("failed to write private key: %w", err)
		}

		if err := os.WriteFile(path+".pub", pub, 0o600); err != nil { //nolint: gomnd
			return fmt.Errorf("failed to write public key: %w", err)
		}
		return nil
	}
}

func getWidth(max int) int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w > max {
		return maxWidth
	}
	return w
}

func renderBlock(w io.Writer, s lipgloss.Style, width int, str string) {
	_, _ = io.WriteString(w, s.Copy().Width(width).Render(str))
	_, _ = io.WriteString(w, "\n")
}

func completeColor(truecolor, ansi256, ansi string) string {
	//nolint: exhaustive
	switch lipgloss.ColorProfile() {
	case termenv.TrueColor:
		return truecolor
	case termenv.ANSI256:
		return ansi256
	}
	return ansi
}

// setLanguage sets the language of the big39 mnemonic seed.
func setLanguage(language string) error {
	list := getWordlist(language)
	if list == nil {
		return fmt.Errorf("this language is not supported")
	}
	bip39.SetWordList(list)
	return nil
}

func sanitizeLang(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), " ", "-")
}

var wordLists = map[lang.Tag][]string{
	lang.Chinese:              wordlists.ChineseSimplified,
	lang.SimplifiedChinese:    wordlists.ChineseSimplified,
	lang.TraditionalChinese:   wordlists.ChineseTraditional,
	lang.Czech:                wordlists.Czech,
	lang.AmericanEnglish:      wordlists.English,
	lang.BritishEnglish:       wordlists.English,
	lang.English:              wordlists.English,
	lang.French:               wordlists.French,
	lang.Italian:              wordlists.Italian,
	lang.Japanese:             wordlists.Japanese,
	lang.Korean:               wordlists.Korean,
	lang.Spanish:              wordlists.Spanish,
	lang.EuropeanSpanish:      wordlists.Spanish,
	lang.LatinAmericanSpanish: wordlists.Spanish,
}

func getWordlist(language string) []string {
	language = sanitizeLang(language)
	tag := lang.Make(language)
	en := display.English.Languages() // default language name matcher
	for t := range wordLists {
		if sanitizeLang(en.Name(t)) == language {
			tag = t
			break
		}
	}
	if tag == lang.Und { // Unknown language
		return nil
	}
	base, _ := tag.Base()
	btag := lang.MustParse(base.String())
	wl := wordLists[tag]
	if wl == nil {
		return wordLists[btag]
	}
	return wl
}

func readPassword(msg string) ([]byte, error) {
	_, _ = fmt.Fprint(os.Stderr, msg)
	t, err := tty.Open()
	if err != nil {
		return nil, fmt.Errorf("could not open tty: %w", err)
	}
	defer t.Close() //nolint: errcheck
	pass, err := term.ReadPassword(int(t.Input().Fd()))
	if err != nil {
		return nil, fmt.Errorf("could not read passphrase: %w", err)
	}
	return pass, nil
}

func askKeyPassphrase(path string) ([]byte, error) {
	defer fmt.Fprintf(os.Stderr, "\n")
	return readPassword(fmt.Sprintf("Enter the passphrase to unlock %q: ", path))
}

func askNewPassphrase() ([]byte, error) {
	defer fmt.Fprintf(os.Stderr, "\n")
	pass, err := readPassword("Enter new passphrase (empty for no passphrase): ")
	if err != nil {
		return nil, err
	}

	confirm, err := readPassword("\nEnter same passphrase again: ")
	if err != nil {
		return nil, fmt.Errorf("could not read password confirmation for key: %w", err)
	}

	if !bytes.Equal(pass, confirm) {
		return nil, fmt.Errorf("Passphareses do not match")
	}

	return pass, nil
}
