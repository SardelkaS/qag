-- ICU supplies Russian script ordering consistently across server clients.
CREATE COLLATION qag.russian (provider = icu, locale = 'ru-u-ks-level2', deterministic = false);
