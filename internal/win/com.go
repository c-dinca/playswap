//go:build windows

package win

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Minimal hand-written COM: just the vtable slots we call.

type comObj struct{ vtbl *[64]uintptr }

// call invokes a vtable slot. uintptrescapes keeps any Go memory whose
// address is converted in the argument list alive for the duration of the call.
//
//go:uintptrescapes
func (o *comObj) call(slot int, args ...uintptr) uintptr {
	all := append([]uintptr{uintptr(unsafe.Pointer(o))}, args...)
	r, _, _ := syscall.SyscallN(o.vtbl[slot], all...)
	return r
}

func (o *comObj) release() {
	if o != nil {
		o.call(2)
	}
}

func hr(r uintptr) error {
	if int32(r) < 0 {
		return fmt.Errorf("COM error 0x%08X", uint32(r))
	}
	return nil
}

func mustGUID(s string) *windows.GUID {
	g, err := windows.GUIDFromString(s)
	if err != nil {
		panic(err)
	}
	return &g
}

var (
	clsidShellLink      = mustGUID("{00021401-0000-0000-C000-000000000046}")
	iidShellLinkW       = mustGUID("{000214F9-0000-0000-C000-000000000046}")
	iidPersistFile      = mustGUID("{0000010B-0000-0000-C000-000000000046}")
	clsidFileOpenDialog = mustGUID("{DC1C5A9C-E88A-4dde-A5A1-60F82A20AEF7}")
	iidFileOpenDialog   = mustGUID("{d57c7288-d4ad-4768-be02-9d969532d960}")
	iidShellItem        = mustGUID("{43826d1e-e718-42ee-bc55-a1e261c37bfe}")
)

const (
	clsctxInproc         = 0x1
	coinitApartment      = 0x2
	coinitDisableOle1DDE = 0x4
)

// withCOM runs f on a locked thread with COM initialised (STA).
func withCOM(f func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, _, _ := procCoInitializeEx.Call(0, coinitApartment|coinitDisableOle1DDE)
	// S_OK / S_FALSE: initialised here (balance it); RPC_E_CHANGED_MODE: already MTA, still usable.
	if int32(r) >= 0 {
		defer windows.CoUninitialize()
	}
	return f()
}

func createInstance(clsid, iid *windows.GUID) (*comObj, error) {
	var obj *comObj
	r, _, _ := procCoCreateInstance.Call(uintptr(unsafe.Pointer(clsid)), 0, clsctxInproc,
		uintptr(unsafe.Pointer(iid)), uintptr(unsafe.Pointer(&obj)))
	if err := hr(r); err != nil {
		return nil, err
	}
	if obj == nil {
		return nil, errors.New("CoCreateInstance returned nil")
	}
	return obj, nil
}

// wstr converts to a NUL-terminated UTF-16 pointer. Always convert it to
// uintptr inside the call's argument list so it stays alive during the call.
func wstr(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

type Shortcut struct {
	Path        string // .lnk to create
	Target      string
	Args        string
	WorkDir     string
	Icon        string
	IconIndex   int
	Description string
}

// CreateShortcut writes a .lnk file through IShellLinkW.
func CreateShortcut(s Shortcut) error {
	return withCOM(func() error {
		link, err := createInstance(clsidShellLink, iidShellLinkW)
		if err != nil {
			return err
		}
		defer link.release()
		// IShellLinkW: 7 SetDescription, 9 SetWorkingDirectory, 11 SetArguments, 17 SetIconLocation, 20 SetPath
		if err := hr(link.call(20, uintptr(unsafe.Pointer(wstr(s.Target))))); err != nil {
			return err
		}
		if s.Args != "" {
			link.call(11, uintptr(unsafe.Pointer(wstr(s.Args))))
		}
		if s.WorkDir != "" {
			link.call(9, uintptr(unsafe.Pointer(wstr(s.WorkDir))))
		}
		if s.Description != "" {
			link.call(7, uintptr(unsafe.Pointer(wstr(s.Description))))
		}
		if s.Icon != "" {
			link.call(17, uintptr(unsafe.Pointer(wstr(s.Icon))), uintptr(s.IconIndex))
		}
		var pf *comObj
		if err := hr(link.call(0, uintptr(unsafe.Pointer(iidPersistFile)), uintptr(unsafe.Pointer(&pf)))); err != nil {
			return err
		}
		defer pf.release()
		// IPersistFile: 6 Save(pszFileName, fRemember)
		return hr(pf.call(6, uintptr(unsafe.Pointer(wstr(s.Path))), 1))
	})
}

type FileFilter struct{ Name, Spec string }

// BrowseOptions configures the native Explorer picker.
type BrowseOptions struct {
	Owner   uintptr
	Title   string
	Folder  bool
	Filters []FileFilter
	Start   string // folder to open in
}

var ErrCancelled = errors.New("cancelled")

// Browse shows the modern IFileOpenDialog (file or folder) and returns the
// chosen path, or ErrCancelled.
func Browse(o BrowseOptions) (string, error) {
	var result string
	err := withCOM(func() error {
		dlg, err := createInstance(clsidFileOpenDialog, iidFileOpenDialog)
		if err != nil {
			return err
		}
		defer dlg.release()
		const (
			fosPickFolders     = 0x20
			fosForceFileSystem = 0x40
			fosPathMustExist   = 0x800
			fosFileMustExist   = 0x1000
		)
		var opts uint32
		dlg.call(10, uintptr(unsafe.Pointer(&opts))) // GetOptions
		opts |= fosForceFileSystem | fosPathMustExist
		if o.Folder {
			opts |= fosPickFolders
		} else {
			opts |= fosFileMustExist
		}
		dlg.call(9, uintptr(opts)) // SetOptions
		if o.Title != "" {
			dlg.call(17, uintptr(unsafe.Pointer(wstr(o.Title)))) // SetTitle
		}
		if !o.Folder && len(o.Filters) > 0 {
			type spec struct{ name, spec *uint16 }
			specs := make([]spec, len(o.Filters))
			for i, f := range o.Filters {
				specs[i].name, _ = windows.UTF16PtrFromString(f.Name)
				specs[i].spec, _ = windows.UTF16PtrFromString(f.Spec)
			}
			dlg.call(4, uintptr(len(specs)), uintptr(unsafe.Pointer(&specs[0]))) // SetFileTypes
			runtime.KeepAlive(specs)
		}
		if o.Start != "" && procSHCreateItemFromParsingName.Find() == nil {
			var item *comObj
			r, _, _ := procSHCreateItemFromParsingName.Call(uintptr(unsafe.Pointer(wstr(o.Start))), 0, uintptr(unsafe.Pointer(iidShellItem)), uintptr(unsafe.Pointer(&item)))
			if hr(r) == nil && item != nil {
				dlg.call(12, uintptr(unsafe.Pointer(item))) // SetFolder
				item.release()
			}
		}
		r := dlg.call(3, o.Owner) // Show
		if uint32(r) == 0x800704C7 {
			return ErrCancelled
		}
		if err := hr(r); err != nil {
			return err
		}
		var item *comObj
		if err := hr(dlg.call(20, uintptr(unsafe.Pointer(&item)))); err != nil { // GetResult
			return err
		}
		defer item.release()
		var p *uint16
		const sigdnFileSysPath = 0x80058000
		if err := hr(item.call(5, sigdnFileSysPath, uintptr(unsafe.Pointer(&p)))); err != nil { // GetDisplayName
			return err
		}
		result = windows.UTF16PtrToString(p)
		procCoTaskMemFree.Call(uintptr(unsafe.Pointer(p)))
		return nil
	})
	return result, err
}

func syscallN(p uintptr, args ...uintptr) uintptr {
	r, _, _ := syscall.SyscallN(p, args...)
	return r
}
