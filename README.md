Records xsel -b, that's it

I use it with a systemd --user service:
```
[Unit]
Description=GoClip
After=syslog.target

[Service]
Type=simple
ExecStart=/home/kobe/dev/go/bin/goclip
PrivateTmp=true
ProtectSystem=full
NoNewPrivileges=true

[Install]
WantedBy=default.target
```

and an .xbinkeysrc entry (st is my terminal and -c sets the window class so my wm can apply specific rules to it):

```
"st -c clipmenu -e bash -ic 'goclip | fzf | goclip -d'"
    m:0x41 + c:33
    Shift+Mod4 + p
```
