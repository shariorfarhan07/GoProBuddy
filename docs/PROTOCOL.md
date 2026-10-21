# How GoProBuddy talks to a GoPro

This page explains the camera protocol that GoProBuddy uses. Everything here comes from two sources:

- **[Open GoPro](https://gopro.github.io/OpenGoPro/)**, GoPro's public API documentation for HERO9 and later, marked **[OGP]**.
- **What we measured on a real camera**, a HERO9 Black running firmware HD9.01.01.72.00, marked **[HERO9]**.

GoProBuddy is an independent project and isn't affiliated with GoPro, Inc.

---

## 1. Bluetooth (BLE)  [OGP]

### Finding the camera
- Cameras advertise service **`0xFEA6`**, under the name `GoPro XXXX`, with manufacturer data that uses GoPro's Bluetooth company ID **`0x02F2`**.
- On Windows, a 16-bit UUID in an advertisement is sometimes expanded with the wrong base. GoProBuddy therefore compares only the first 32 bits of each UUID.

### Pairing
- The camera only shows its control characteristics over an **encrypted, bonded** link.
- For the first connection, put the camera in pairing mode (*Preferences → Wireless Connections → Connect Device*).
- GoProBuddy bonds through the Windows pairing API, then connects over GATT.

### Characteristics used
All of them share the suffix `-aa8d-11e3-9046-0002a5d5c51b`.

| Characteristic | UUID prefix | Used for |
|---|---|---|
| Wi-Fi AP SSID | `b5f90002` | Read the camera's Wi-Fi network name |
| Wi-Fi AP password | `b5f90003` | Read the camera's Wi-Fi password |
| Command | `b5f90072` | Write commands |
| Command response | `b5f90073` | Notifications |
| Settings | `b5f90074` | Write settings |
| Settings response | `b5f90075` | Notifications |

### Messages
Short messages fit in one packet. The first byte is the payload length (the 5-bit "general" header), followed by the payload.

| Purpose | Bytes |
|---|---|
| Turn the camera's Wi-Fi on | `03 17 01 01` on Command |
| Keep the camera awake (every ~3 s) | `03 5B 01 42` on Settings |

The camera answers on the matching response characteristic. For example, turning Wi-Fi on returns `02 17 00`, where `00` means success. **[HERO9]** confirmed this.

---

## 2. Wi-Fi  [OGP]

- The camera runs its own access point. **The camera is always `10.5.5.9`**, and DHCP gives the PC an address in `10.5.5.0/24`. **[HERO9]** gave the PC `10.5.5.100`.
- Commands use plain HTTP on **port 80**. Media files are served on **port 8080**. There's no authentication.
- `netsh wlan connect` only *queues* a connection request. GoProBuddy waits until Windows reports it's on the camera's network **and** has a `10.5.5.x` address. Until then, requests go out over another adapter and time out.

### USB-C
A camera plugged in with USB-C (with *USB Connection* set to *GoPro Connect*) appears as a network adapter. The PC gets an address in `172.2X.1YZ.0/24`, and the camera is at `172.2X.1YZ.51`. Before using webcam mode over USB, send `GET /gopro/camera/control/wired_usb?p=1`.

---

## 3. HTTP API

### Camera state  [OGP]
| Request | Returns |
|---|---|
| `GET /gopro/camera/state` | `{"status":{id:value}, "settings":{id:value}}` |
| `GET /gopro/camera/keep_alive` | Keeps the camera awake. Call it every few seconds. |
| `GET /gopro/camera/info` | Model and firmware. **[HERO9]** returns 404. |
| `GET /gp/gpControl/info` | Model and firmware on older firmware. **[HERO9]** supports it. |

Status IDs that GoProBuddy reads:

| ID | Meaning |
|---|---|
| 10 | Recording (encoding) |
| 8 | Busy. This is also set in webcam mode, so it **doesn't** mean recording. |
| 70 | Battery % |
| 54 | Free SD space, in KB |
| 33 | SD card status. `2` means no card. |
| 89 | Current mode, used by the settings filters |

### Media  [OGP]
| Request | Notes |
|---|---|
| `GET /gopro/media/list` | `{"media":[{"d":"100GOPRO","fs":[{"n","s","cre",…}]}]}`. Sizes and times are strings. Group entries (burst, time-lapse) have `b` (first index) and `l` (last index). |
| `GET /gopro/media/thumbnail?path=100GOPRO/GX010001.MP4` | JPEG thumbnail |
| `GET http://10.5.5.9:8080/videos/DCIM/<dir>/<file>` | The full file. Supports `Range`, so downloads can resume. |
| `GET /gopro/media/turbo_transfer?p=1` | Faster transfers. Turn it off afterwards with `p=0`. |

**[HERO9]** With no SD card, both `/gopro/media/list` and `/gp/gpMediaList` return 404 with `{"message":"Unknown Error."}`. GoProBuddy checks status 33 to explain this to the user.

### Shutter  [OGP]
`GET /gopro/camera/shutter/start` and `GET /gopro/camera/shutter/stop`.

---

## 4. Settings

### Reading the settings
`GET /gp/gpControl` returns the camera's own description of its settings, so GoProBuddy shows exactly what the connected model and firmware support.

- **`settings`**: `[{id, display_name, options:[{display_name, value}]}]`. **[HERO9]** firmware names the option number `id` instead of `value`, so GoProBuddy accepts both.
- **`display_hints`**: menu groups (`Mode Settings`, `Protune`, `Setup` …), each listing `setting_id`s in menu order. GoProBuddy uses these groups and their order, and hides settings that aren't in any group.
- **`filters`**: availability rules, `{activated_by:[{status_id, values} | {setting_id, ids}], blacklist:[{setting_id, opt_ids}]}`. When every condition in a filter holds, its blacklisted options aren't allowed. A setting whose options are all blacklisted doesn't apply in the current mode. **[HERO9]** in Photo mode: 30 of 71 settings apply.

The current values are under `settings` in `/gopro/camera/state`.

### Changing a setting  [OGP]
`GET /gopro/camera/setting?setting=<id>&option=<value>`. On older firmware the fallback is `/gp/gpControl/setting/<id>/<value>`.

**[HERO9]** behaviour:
- An invalid option returns **403**.
- Setting the value a setting **already has** returns **500**, so GoProBuddy skips that case.
- After a change, read the state again, because other settings can change with it.

---

## 5. Live preview  [OGP]
1. `GET /gopro/camera/stream/start?port=8556`
2. Every 2.5 s, send the UDP datagram `_GPHD_:0:0:2:0.000000\n` to `10.5.5.9:8554`, or the stream stops.
3. Receive **MPEG-TS** (H.264) on local UDP port **8556**. GoProBuddy decodes it with `ffmpeg`.
4. `GET /gopro/camera/stream/stop`

---

## 6. Webcam  [OGP]
| Request | Notes |
|---|---|
| `GET /gopro/webcam/start?res=12&fov=0&port=8554&protocol=TS` | `res`: 12 = 1080p, 7 = 720p, 4 = 480p. `fov`: 0 = Wide, 4 = Linear, 2 = Narrow, 3 = SuperView. |
| `GET /gopro/webcam/stop`, `/gopro/webcam/exit` | Leave webcam mode |
| `GET /gopro/webcam/status` | `status`: 0 = off, 1 = idle, 2 = streaming |
| `GET /gopro/webcam/version` | **[HERO9]** `{"version":4,…}` |

Every reply has the form `{"status":N,"error":N}`. The error codes are: 1 preset, 2 resolution, 3 stream, 4 busy (shutter), 5 timeout, 6 invalid parameter, 7 unavailable, 8 exiting.

What we measured on **[HERO9]**:
- Webcam mode **works over Wi-Fi**.
- It **ignores the `port` parameter and always streams to UDP 8554**, about 6 Mbit/s of MPEG-TS. GoProBuddy therefore always requests port 8554.
- Right after connecting, the first `start` can return error **7** (unavailable) and then succeed a moment later. GoProBuddy retries once.
