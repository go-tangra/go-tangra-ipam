package hostsync

import (
	"fmt"

	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostplan"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/hostreport"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/repo"
	"github.com/go-tangra/go-tangra-ipam/v4/internal/store"
)

// load reads, inside the apply transaction, everything the planner needs
// for one report.
func load(tx repo.HostTx, r hostreport.Report) (hostplan.State, error) {
	var st hostplan.State
	d, ok, err := tx.DeviceByInventoryHost(r.HostID)
	if err != nil {
		return st, err
	}
	if ok {
		st.Candidates.ByHost = &d
	} else if !hostplan.IsPlaceholderSerial(r.Serial) {
		if st.Candidates.BySerial, err = tx.DevicesBySerial(r.Serial); err != nil {
			return st, err
		}
	}
	if st.Candidates.ByName, err = tx.DevicesByNames(hostplan.CandidateNames(r)); err != nil {
		return st, err
	}
	st.Addresses = map[string]store.IPAddress{}
	if dev, _ := hostplan.Match(st.Candidates, r); dev != nil {
		if st.Interfaces, err = tx.Interfaces(dev.ID); err != nil {
			return st, err
		}
		owned, err := tx.AddressesOfDevice(dev.ID)
		if err != nil {
			return st, err
		}
		for _, a := range owned {
			st.Addresses[a.Address] = a
		}
		if st.Packages, err = tx.Packages(dev.ID); err != nil {
			return st, err
		}
		if st.Guests, err = tx.Guests(dev.ID); err != nil {
			return st, err
		}
		if st.GuestDevices, err = tx.GuestDevicesOf(dev.ID); err != nil {
			return st, err
		}
	}
	var addrs, ownMACs, guestMACs []string
	for _, i := range r.Interfaces {
		for _, a := range i.Addresses {
			addrs = append(addrs, a.Addr.String())
		}
		if i.MAC != "" {
			ownMACs = append(ownMACs, i.MAC)
		}
	}
	if r.BMC != nil && r.BMC.Address.IsValid() {
		addrs = append(addrs, r.BMC.Address.String())
	}
	if len(addrs) > 0 {
		rows, err := tx.AddressesByValue(addrs)
		if err != nil {
			return st, err
		}
		for _, a := range rows {
			st.Addresses[a.Address] = a
		}
	}
	if st.Subnets, err = tx.Subnets(); err != nil {
		return st, err
	}
	for _, g := range r.Guests {
		guestMACs = append(guestMACs, g.MACs...)
	}
	if len(guestMACs) > 0 {
		if st.MACOwners, err = tx.DevicesByMAC(guestMACs); err != nil {
			return st, err
		}
	}
	if len(ownMACs) > 0 {
		if st.GuestRows, err = tx.GuestRowsByMAC(ownMACs); err != nil {
			return st, err
		}
	}
	return st, nil
}

// execute performs the planned writes in order, each followed by its audit
// rows, inside the apply transaction.
func execute(tx repo.HostTx, p hostplan.Plan) error {
	for _, op := range p.Ops {
		var err error
		switch op.Kind {
		case hostplan.OpCreateDevice:
			err = tx.InsertDevice(*op.Device)
		case hostplan.OpUpdateDevice:
			err = tx.UpdateDeviceReported(*op.Device)
		case hostplan.OpCreateInterface:
			err = tx.UpsertInterfaceReported(*op.Interface, true)
		case hostplan.OpUpdateInterface:
			err = tx.UpsertInterfaceReported(*op.Interface, false)
		case hostplan.OpCreateSubnet:
			err = tx.CreateSubnetAuto(*op.Subnet)
		case hostplan.OpCreateAddress:
			err = tx.InsertAddressReported(*op.Address)
		case hostplan.OpUpdateAddress:
			err = tx.UpdateAddressReported(*op.Address)
		case hostplan.OpReplacePackages:
			err = tx.ReplacePendingPackages(op.DeviceID, op.Packages)
		case hostplan.OpReplaceGuests:
			err = tx.ReplaceGuests(op.DeviceID, op.Guests)
		case hostplan.OpSetHypervisor:
			err = tx.SetHypervisor(op.DeviceID, op.RefID)
		case hostplan.OpSetGuestDevice:
			err = tx.SetGuestDevice(op.GuestRowID, op.RefID)
		default:
			err = fmt.Errorf("hostsync: unknown op %q", op.Kind)
		}
		if err != nil {
			return fmt.Errorf("hostsync: %s: %w", op.Kind, err)
		}
		for _, row := range op.Audit {
			if err := tx.AppendAudit(row); err != nil {
				return fmt.Errorf("hostsync: audit: %w", err)
			}
		}
	}
	return nil
}
