package trackers

var (
  	Out io.Writer = os.Stdout
  	Err io.Writer = os.Stderr
)

func Init(out, err io.Writer) {
	Out = out
	Err = err
}
