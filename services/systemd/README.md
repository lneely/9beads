# systemd Service Units for 9beads

## Service Files

### `9beads-user.service` - User Service (Recommended)

Run 9beads as a user service.

```sh
mkdir -p ~/.config/systemd/user
cp services/systemd/9beads-user.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now 9beads-user.service
```

**Enable Linger (Start on Boot):**
```sh
sudo loginctl enable-linger $USER
```

### `9beads@.service` - System Service Template

Run 9beads as a system service for specific users.

```sh
sudo cp services/systemd/9beads@.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now 9beads@username.service
```

## Management

```sh
# User service
systemctl --user status 9beads-user
journalctl --user -u 9beads-user -f

# System service
sudo systemctl status 9beads@username
sudo journalctl -u 9beads@username -f
```
