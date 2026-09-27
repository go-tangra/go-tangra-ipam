-- +goose Up
-- SNMPv3 AES-192/256 exist in two key-localisation variants: Blumenthal
-- (AES192/AES256) and Reeder (AES192C/AES256C, used by Cisco and others).
-- A device configured for one does not decrypt the other, so both are offered.
ALTER TABLE ipam_subnet_snmp DROP CONSTRAINT subnet_snmp_priv;
ALTER TABLE ipam_subnet_snmp ADD CONSTRAINT subnet_snmp_priv CHECK (
  (security_level = 'authPriv' AND priv_protocol IN ('DES', 'AES', 'AES192', 'AES256', 'AES192C', 'AES256C'))
  OR (security_level <> 'authPriv' AND priv_protocol = ''));

-- +goose Down
ALTER TABLE ipam_subnet_snmp DROP CONSTRAINT subnet_snmp_priv;
ALTER TABLE ipam_subnet_snmp ADD CONSTRAINT subnet_snmp_priv CHECK (
  (security_level = 'authPriv' AND priv_protocol IN ('DES', 'AES', 'AES192', 'AES256'))
  OR (security_level <> 'authPriv' AND priv_protocol = ''));
