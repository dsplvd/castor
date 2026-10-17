package whisper

// A bounded tail preserves native failure diagnostics without allowing a noisy
// library to grow parent memory indefinitely. exec.Cmd has one stderr copier;
// the owner reads these bytes only after cmd.Wait joins that copier.
type workerDiagnostics struct {
	tail []byte
	head []byte
}

func (d *workerDiagnostics) Write(p []byte) (int, error) {
	n := len(p)
	if len(d.head) < 8192 {
		d.head = append(d.head, p[:min(len(p), 8192-len(d.head))]...)
	}
	const limit = 4096
	if len(p) >= limit {
		d.tail = append(d.tail[:0], p[len(p)-limit:]...)
	} else {
		if len(d.tail)+len(p) > limit {
			d.tail = d.tail[len(d.tail)+len(p)-limit:]
		}
		d.tail = append(d.tail, p...)
	}
	return n, nil
}
