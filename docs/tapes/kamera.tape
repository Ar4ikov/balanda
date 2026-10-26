# Камера: стена изоляции, лимит времени, фоновая камера, перекличка, шмон, отбой.
Output docs/gifs/kamera.gif
Set Shell "bash"
Set FontSize 15
Set Width 960
Set Height 540
Set Padding 14
Set Framerate 20
Set TypingSpeed 40ms
Set Theme "Dracula"
Set CursorBlink false

Set Height 640
Hide
Type ". docs/tapes/demo.sh" Enter
Show

Type "balanda sadit -- touch /etc/balanda" Sleep 300ms Enter
Sleep 2500ms
Type "balanda sadit --paika cpu=0.5,time=2s -- sleep 30" Sleep 300ms Enter
Sleep 3s
Type "balanda sadit --fon --imya sborka --paika mem=512m -- sh -c 'sleep 300'" Sleep 300ms Enter
Sleep 1500ms
Type "balanda pereklichka" Sleep 300ms Enter
Sleep 2500ms
Type "clear" Enter
Type "balanda shmon sborka" Sleep 300ms Enter
Sleep 4s
Type "balanda otboy" Sleep 300ms Enter
Sleep 3s
