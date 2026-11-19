#include "power.h"

#include <stdbool.h>
#include <stdint.h>

#include "driver/gpio.h"
#include "driver/usb_serial_jtag.h"
#include "esp_adc/adc_cali_scheme.h"
#include "esp_adc/adc_oneshot.h"
#include "esp_sleep.h"
#include "esp_timer.h"

#define VBUS_GPIO GPIO_NUM_0
#define BATTERY_CHANNEL ADC_CHANNEL_1
#define BATTERY_ATTEN ADC_ATTEN_DB_12
#define UNPLUG_DEBOUNCE_US (2LL * 1000 * 1000)
#define FALLBACK_WAKE_US (120ULL * 1000 * 1000)
#define BATTERY_SAMPLES 16

static bool vbus_ready;
static int64_t unplugged_since;
static bool adc_attempted;
static adc_oneshot_unit_handle_t battery_adc;
static adc_cali_handle_t battery_calibration;

void power_init(void)
{
    const gpio_config_t input = {
        .pin_bit_mask = 1ULL << VBUS_GPIO,
        .mode = GPIO_MODE_INPUT,
        .pull_up_en = GPIO_PULLUP_DISABLE,
        .pull_down_en = GPIO_PULLDOWN_DISABLE,
        .intr_type = GPIO_INTR_DISABLE,
    };
    vbus_ready = gpio_config(&input) == ESP_OK;
}

int power_usb_present(void)
{
    return vbus_ready ? gpio_get_level(VBUS_GPIO) : -1;
}

void power_sleep_if_unplugged(void)
{
    if (power_usb_present() != 0 || usb_serial_jtag_is_connected()) {
        unplugged_since = 0;
        return;
    }
    int64_t now = esp_timer_get_time();
    if (unplugged_since == 0) {
        unplugged_since = now;
        return;
    }
    if (now - unplugged_since < UNPLUG_DEBOUNCE_US) {
        return;
    }
    if (esp_sleep_enable_timer_wakeup(FALLBACK_WAKE_US) == ESP_OK &&
        esp_deep_sleep_enable_gpio_wakeup(1ULL << VBUS_GPIO, ESP_GPIO_WAKEUP_GPIO_HIGH) == ESP_OK &&
        power_usb_present() == 0 && !usb_serial_jtag_is_connected()) {
        esp_deep_sleep_try_to_start();
    }
    unplugged_since = 0;
}

static void init_battery_adc(void)
{
    adc_attempted = true;
    const adc_oneshot_unit_init_cfg_t unit_config = {
        .unit_id = ADC_UNIT_1,
        .ulp_mode = ADC_ULP_MODE_DISABLE,
    };
    if (adc_oneshot_new_unit(&unit_config, &battery_adc) != ESP_OK) {
        return;
    }
    const adc_oneshot_chan_cfg_t channel_config = {
        .atten = BATTERY_ATTEN,
        .bitwidth = ADC_BITWIDTH_DEFAULT,
    };
    const adc_cali_curve_fitting_config_t calibration_config = {
        .unit_id = ADC_UNIT_1,
        .chan = BATTERY_CHANNEL,
        .atten = BATTERY_ATTEN,
        .bitwidth = ADC_BITWIDTH_DEFAULT,
    };
    if (adc_oneshot_config_channel(battery_adc, BATTERY_CHANNEL, &channel_config) != ESP_OK ||
        adc_cali_create_scheme_curve_fitting(&calibration_config, &battery_calibration) != ESP_OK) {
        adc_oneshot_del_unit(battery_adc);
        battery_adc = NULL;
    }
}

int power_battery_mv(void)
{
    if (!adc_attempted) {
        init_battery_adc();
    }
    if (battery_adc == NULL || battery_calibration == NULL) {
        return -1;
    }
    int sum = 0;
    for (int sample = 0; sample < BATTERY_SAMPLES; ++sample) {
        int raw;
        if (adc_oneshot_read(battery_adc, BATTERY_CHANNEL, &raw) != ESP_OK) {
            return -1;
        }
        sum += raw;
    }
    int mv;
    if (adc_cali_raw_to_voltage(battery_calibration, (sum + BATTERY_SAMPLES / 2) / BATTERY_SAMPLES, &mv) != ESP_OK) {
        return -1;
    }
    mv *= 2;
    return mv >= 2500 && mv <= 4500 ? mv : -1;
}
