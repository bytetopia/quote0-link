#ifndef QUOTE0_LINK_POWER_H
#define QUOTE0_LINK_POWER_H

void power_init(void);
void power_sleep_if_unplugged(void);
int power_usb_present(void);
int power_battery_mv(void);

#endif
