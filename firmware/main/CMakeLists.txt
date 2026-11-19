set(driver "$ENV{QUOTE0_PLATFORM}/devices/quote_0/firmware-resources/display/drivers")
if(NOT EXISTS "${driver}/uc8251d_minimal.c")
    message(FATAL_ERROR "Set QUOTE0_PLATFORM to the MindReset dot_open_platform checkout (see firmware/README.md)")
endif()

idf_component_register(
    SRCS "main.c" "power.c" "${driver}/uc8251d_minimal.c"
    INCLUDE_DIRS "${driver}"
    PRIV_REQUIRES esp_driver_gpio esp_driver_spi esp_driver_usb_serial_jtag esp_timer esp_adc esp_hw_support esp_app_format
)
