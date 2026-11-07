# powerlog

Logs power and temperature of an AMD APU to a CSV file, one row per second.
Made for the GMKtec EVO-X2 (Ryzen AI Max+ 395) used as an inference server.

## How it works

- Reads kernel sensor files in `/sys/class/hwmon`. No root needed.
- Takes 2 samples per second. Writes 1 row per second with the average.
- Runs as a systemd service, as user `powerlog`.
- logrotate rotates the file weekly and keeps 52 weeks. Current and last week are plain CSV. Older weeks are gzipped.
  Rotated files are named `power-YYYYMMDD.csv` (date of rotation).

## Data

File: `/var/log/powerlog/power.csv`

| Column        | Meaning                                       |
|---------------|-----------------------------------------------|
| `time`        | Start of the second, UTC                      |
| `power_w`     | Average chip power (W)                        |
| `power_max_w` | Highest sample in that second (W)             |
| `gpu_temp_c`  | GPU temperature (°C)                          |
| `cpu_temp_c`  | CPU temperature, Tctl (°C)                    |
| `nvme_temp_c` | SSD temperature (°C)                          |
| `gpu_busy_pct`| GPU load (%)                                  |
| `samples`     | Samples in the row. Usually 2, 1 at start/stop |

Energy: `J = sum(power_w × samples × 0.5)`, `Wh = J / 3600`.

Power is for the chip only (CPU + GPU + memory controller). Power from the wall is higher
(RAM, SSD, fans, power supply loss). Use a smart plug if you need real wall power.

## Setup

Needs Go and just from Ubuntu repos:

    sudo apt install golang-go just

Commands:

    just build     # build the binary
    just run       # print rows to the terminal, no log file (Ctrl+C to stop)
    just install   # install and start the service (uses sudo)
    just clean     # remove the binary

Check that it works:

    systemctl status powerlog
    tail -f /var/log/powerlog/power.csv

`install.sh` puts files here:

    /usr/local/bin/powerlog
    /etc/systemd/system/powerlog.service
    /etc/logrotate.d/powerlog

## New machine

The program needs the `amdgpu` sensor. Check with:

    cat /sys/class/hwmon/*/name

If `amdgpu` is not in the list, the program stops with an error. `k10temp` (CPU) and `nvme` are optional.

## Remove

    sudo systemctl disable --now powerlog
    sudo rm /usr/local/bin/powerlog /etc/systemd/system/powerlog.service /etc/logrotate.d/powerlog
    sudo userdel powerlog
    sudo rm -r /var/log/powerlog   # deletes the logs
