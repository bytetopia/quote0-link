#include <stdbool.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "driver/usb_serial_jtag.h"
#include "freertos/FreeRTOS.h"
#include "uc8251d_minimal.h"

static uint8_t frame[UC8251D_BUFFER_SIZE];

static const uc8251d_config_t panel = {
    .spi_host = SPI2_HOST,
    .pin_vin = 20,
    .pin_busy = 3,
    .pin_reset = 4,
    .pin_dc = 5,
    .pin_cs = 6,
    .pin_sck = 10,
    .pin_mosi = 7,
    .spi_clock_hz = 15 * 1000 * 1000,
};

static void reply(const char *text)
{
    usb_serial_jtag_write_bytes(text, strlen(text), pdMS_TO_TICKS(1000));
}

static uint32_t frame_crc(void)
{
    uint32_t crc = UINT32_MAX;
    for (size_t index = 0; index < sizeof(frame); ++index) {
        crc ^= frame[index];
        for (int bit = 0; bit < 8; ++bit) {
            crc = (crc >> 1) ^ ((0U - (crc & 1U)) & 0xEDB88320U);
        }
    }
    return ~crc;
}

static bool read_frame(void)
{
    size_t received = 0;
    while (received < sizeof(frame)) {
        int count = usb_serial_jtag_read_bytes(frame + received,
            sizeof(frame) - received, pdMS_TO_TICKS(1000));
        if (count <= 0) {
            return false;
        }
        received += count;
    }
    return true;
}

static void handle_line(const char *line)
{
    if (strcmp(line, "Q0PING") == 0) {
        reply("Q0LINK 1 152 296\n");
        return;
    }
    const char prefix[] = "Q0IMG1 152 296 1BPP 5624 ";
    const size_t prefix_size = sizeof(prefix) - 1;
    unsigned checksum;
    if (strncmp(line, prefix, prefix_size) != 0 ||
        strlen(line + prefix_size) != 8 ||
        strspn(line + prefix_size, "0123456789abcdefABCDEF") != 8 ||
        sscanf(line + prefix_size, "%x", &checksum) != 1) {
        reply("ERR header\n");
        return;
    }
    if (!read_frame()) {
        reply("ERR incomplete\n");
    } else if (frame_crc() != checksum) {
        reply("ERR checksum\n");
    } else {
        reply(uc8251d_display(frame) == ESP_OK ? "OK\n" : "ERR panel\n");
    }
}

void app_main(void)
{
    usb_serial_jtag_driver_config_t usb = {
        .tx_buffer_size = 512,
        .rx_buffer_size = 8192,
    };
    ESP_ERROR_CHECK(uc8251d_init(&panel));
    ESP_ERROR_CHECK(usb_serial_jtag_driver_install(&usb));

    char line[64];
    size_t length = 0;
    bool overflow = false;
    while (true) {
        char byte;
        if (usb_serial_jtag_read_bytes(&byte, 1, pdMS_TO_TICKS(1000)) != 1) {
            length = 0;
            overflow = false;
            continue;
        }
        if (byte == '\n') {
            line[length] = '\0';
            if (overflow) {
                reply("ERR header\n");
            } else if (length != 0) {
                handle_line(line);
            }
            length = 0;
            overflow = false;
        } else if (length == sizeof(line) - 1) {
            overflow = true;
        } else {
            line[length++] = byte;
        }
    }
}
