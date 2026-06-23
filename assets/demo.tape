# vhs tape — authentic dcon demo. Regenerate with: vhs assets/demo.tape
Output assets/demo.gif

Set Shell zsh
Set FontSize 19
Set Width 1180
Set Height 700
Set Padding 26
Set Framerate 24
Set TypingSpeed 32ms
Set Theme { "name": "dcon", "background": "#0d1117", "foreground": "#e6edf3", "black": "#484f58", "red": "#ff7b72", "green": "#3fb950", "yellow": "#d29922", "blue": "#58a6ff", "magenta": "#bc8cff", "cyan": "#39c5cf", "white": "#b1bac4", "brightBlack": "#6e7681", "brightRed": "#ffa198", "brightGreen": "#56d364", "brightYellow": "#e3b341", "brightBlue": "#79c0ff", "brightMagenta": "#d2a8ff", "brightCyan": "#56d4dd", "brightWhite": "#ffffff", "cursor": "#3fb950", "selection": "#264f78" }

Hide
Type "export PATH=/Users/karthikvinayan/code/work/docontaier:$PATH; export PS1='%F{green}❯%f '; cd /tmp/dcon-demo; clear" Enter
Sleep 800ms
Show

Type "dcon run --rm alpine echo 'hello from dcon 👋'" Sleep 400ms Enter
Sleep 4s

Type "dcon images" Sleep 400ms Enter
Sleep 2500ms

Type "dcon compose up -d" Sleep 400ms Enter
Sleep 7s

Type "dcon compose ps" Sleep 400ms Enter
Sleep 3s

Type "dcon compose down" Sleep 400ms Enter
Sleep 4s

Sleep 1500ms
