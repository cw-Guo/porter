package porter

import (
	"fmt"
	"time"

	"get.porter.sh/porter/pkg/credentials"
	"get.porter.sh/porter/pkg/editor"
	"get.porter.sh/porter/pkg/encoding"
	"get.porter.sh/porter/pkg/generator"
	"get.porter.sh/porter/pkg/printer"
	"get.porter.sh/porter/pkg/storage"
	dtprinter "github.com/carolynvs/datetime-printer"
	"github.com/olekukonko/tablewriter"
	"github.com/pkg/errors"
)

// CredentialShowOptions represent options for Porter's credential show command
type CredentialShowOptions struct {
	printer.PrintOptions
	Name      string
	Namespace string
}

type CredentialEditOptions struct {
	Name      string
	Namespace string
}

// ListCredentials lists saved credential sets.
func (p *Porter) ListCredentials(opts ListOptions) ([]credentials.CredentialSet, error) {
	return p.Credentials.ListCredentialSets(opts.GetNamespace(), opts.Name, opts.ParseLabels())
}

// PrintCredentials prints saved credential sets.
func (p *Porter) PrintCredentials(opts ListOptions) error {
	creds, err := p.ListCredentials(opts)
	if err != nil {
		return err
	}

	switch opts.Format {
	case printer.FormatJson:
		return printer.PrintJson(p.Out, creds)
	case printer.FormatYaml:
		return printer.PrintYaml(p.Out, creds)
	case printer.FormatTable:
		// have every row use the same "now" starting ... NOW!
		now := time.Now()
		tp := dtprinter.DateTimePrinter{
			Now: func() time.Time { return now },
		}

		printCredRow :=
			func(v interface{}) []interface{} {
				cr, ok := v.(credentials.CredentialSet)
				if !ok {
					return nil
				}
				return []interface{}{cr.Namespace, cr.Name, tp.Format(cr.Modified)}
			}
		return printer.PrintTable(p.Out, creds, printCredRow,
			"NAMESPACE", "NAME", "MODIFIED")
	default:
		return fmt.Errorf("invalid format: %s", opts.Format)
	}
}

// CredentialsOptions are the set of options available to Porter.GenerateCredentials
type CredentialOptions struct {
	BundleActionOptions
	Silent bool
	Labels []string
}

func (o CredentialOptions) ParseLabels() map[string]string {
	return parseLabels(o.Labels)
}

// Validate prepares for an action and validates the options.
// For example, relative paths are converted to full paths and then checked that
// they exist and are accessible.
func (o *CredentialOptions) Validate(args []string, p *Porter) error {
	err := o.validateCredName(args)
	if err != nil {
		return err
	}

	return o.BundleActionOptions.Validate(args, p)
}

func (o *CredentialOptions) validateCredName(args []string) error {
	if len(args) == 1 {
		o.Name = args[0]
	} else if len(args) > 1 {
		return errors.Errorf("only one positional argument may be specified, the credential name, but multiple were received: %s", args)
	}
	return nil
}

// GenerateCredentials builds a new credential set based on the given options. This can be either
// a silent build, based on the opts.Silent flag, or interactive using a survey. Returns an
// error if unable to generate credentials
func (p *Porter) GenerateCredentials(opts CredentialOptions) error {
	bundleRef, err := p.resolveBundleReference(&opts.BundleActionOptions)
	if err != nil {
		return err
	}

	name := opts.Name
	if name == "" {
		name = bundleRef.Definition.Name
	}
	genOpts := generator.GenerateCredentialsOptions{
		GenerateOptions: generator.GenerateOptions{
			Name:      name,
			Namespace: opts.Namespace,
			Labels:    opts.ParseLabels(),
			Silent:    opts.Silent,
		},
		Credentials: bundleRef.Definition.Credentials,
	}
	fmt.Fprintf(p.Out, "Generating new credential %s from bundle %s\n", genOpts.Name, bundleRef.Definition.Name)
	fmt.Fprintf(p.Out, "==> %d credentials required for bundle %s\n", len(genOpts.Credentials), bundleRef.Definition.Name)

	cs, err := generator.GenerateCredentials(genOpts)
	if err != nil {
		return errors.Wrap(err, "unable to generate credentials")
	}

	cs.Created = time.Now()
	cs.Modified = cs.Created

	err = p.Credentials.UpsertCredentialSet(cs)
	return errors.Wrapf(err, "unable to save credentials")
}

// Validate validates the args provided to Porter's credential show command
func (o *CredentialShowOptions) Validate(args []string) error {
	if err := validateCredentialName(args); err != nil {
		return err
	}
	o.Name = args[0]
	return o.ParseFormat()
}

// Validate validates the args provided to Porter's credential edit command
func (o *CredentialEditOptions) Validate(args []string) error {
	if err := validateCredentialName(args); err != nil {
		return err
	}
	o.Name = args[0]
	return nil
}

// EditCredential edits the credentials of the provided name.
func (p *Porter) EditCredential(opts CredentialEditOptions) error {
	credSet, err := p.Credentials.GetCredentialSet(opts.Namespace, opts.Name)
	if err != nil {
		return err
	}

	// TODO(carolynvs): support editing in yaml, json or toml
	contents, err := encoding.MarshalYaml(credSet)
	if err != nil {
		return errors.Wrap(err, "unable to load credentials")
	}

	editor := editor.New(p.Context, fmt.Sprintf("porter-%s.yaml", credSet.Name), contents)
	output, err := editor.Run()
	if err != nil {
		return errors.Wrap(err, "unable to open editor to edit credentials")
	}

	err = encoding.UnmarshalYaml(output, &credSet)
	if err != nil {
		return errors.Wrap(err, "unable to process credentials")
	}

	err = p.Credentials.Validate(credSet)
	if err != nil {
		return errors.Wrap(err, "credentials are invalid")
	}

	credSet.Modified = time.Now()
	err = p.Credentials.UpdateCredentialSet(credSet)
	if err != nil {
		return errors.Wrap(err, "unable to save credentials")
	}

	return nil
}

// ShowCredential shows the credential set corresponding to the provided name, using
// the provided printer.PrintOptions for display.
func (p *Porter) ShowCredential(opts CredentialShowOptions) error {
	credSet, err := p.Credentials.GetCredentialSet(opts.Namespace, opts.Name)
	if err != nil {
		return err
	}

	switch opts.Format {
	case printer.FormatJson, printer.FormatYaml:
		result, err := encoding.Marshal(string(opts.Format), credSet)
		if err != nil {
			return err
		}
		fmt.Fprintln(p.Out, string(result))
		return nil
	case printer.FormatTable:
		// Set up human friendly time formatter
		now := time.Now()
		tp := dtprinter.DateTimePrinter{
			Now: func() time.Time { return now },
		}

		// Here we use an instance of olekukonko/tablewriter as our table,
		// rather than using the printer pkg variant, as we wish to decorate
		// the table a bit differently from the default
		var rows [][]string

		// Iterate through all CredentialStrategies and add to rows
		for _, cs := range credSet.Credentials {
			rows = append(rows, []string{cs.Name, cs.Source.Value, cs.Source.Key})
		}

		// Build and configure our tablewriter
		table := tablewriter.NewWriter(p.Out)
		table.SetCenterSeparator("")
		table.SetColumnSeparator("")
		table.SetAlignment(tablewriter.ALIGN_LEFT)
		table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
		table.SetBorders(tablewriter.Border{Left: false, Right: false, Bottom: false, Top: true})
		table.SetAutoFormatHeaders(false)

		// First, print the CredentialSet metadata
		fmt.Fprintf(p.Out, "Name: %s\n", credSet.Name)
		fmt.Fprintf(p.Out, "Namespace: %s\n", credSet.Namespace)
		fmt.Fprintf(p.Out, "Created: %s\n", tp.Format(credSet.Created))
		fmt.Fprintf(p.Out, "Modified: %s\n\n", tp.Format(credSet.Modified))

		// Print labels, if any
		if len(credSet.Labels) > 0 {
			fmt.Fprintln(p.Out, "Labels:")

			for k, v := range credSet.Labels {
				fmt.Fprintf(p.Out, "  %s: %s\n", k, v)
			}
			fmt.Fprintln(p.Out)
		}

		// Now print the table
		table.SetHeader([]string{"Name", "Local Source", "Source Type"})
		for _, row := range rows {
			table.Append(row)
		}
		table.Render()
		return nil
	default:
		return fmt.Errorf("invalid format: %s", opts.Format)
	}
}

// CredentialDeleteOptions represent options for Porter's credential delete command
type CredentialDeleteOptions struct {
	Name      string
	Namespace string
}

// DeleteCredential deletes the credential set corresponding to the provided
// names.
func (p *Porter) DeleteCredential(opts CredentialDeleteOptions) error {
	err := p.Credentials.RemoveCredentialSet(opts.Namespace, opts.Name)
	if errors.Is(err, storage.ErrNotFound{}) {
		if p.Debug {
			fmt.Fprintln(p.Err, err)
		}
		return nil
	}
	return errors.Wrapf(err, "unable to delete credential set")
}

// Validate validates the args provided Porter's credential delete command
func (o *CredentialDeleteOptions) Validate(args []string) error {
	if err := validateCredentialName(args); err != nil {
		return err
	}
	o.Name = args[0]
	return nil
}

func validateCredentialName(args []string) error {
	switch len(args) {
	case 0:
		return errors.Errorf("no credential name was specified")
	case 1:
		return nil
	default:
		return errors.Errorf("only one positional argument may be specified, the credential name, but multiple were received: %s", args)
	}
}

func (p *Porter) CredentialsApply(o ApplyOptions) error {
	namespace, err := p.getNamespaceFromFile(o)
	if err != nil {
		return err
	}

	var creds credentials.CredentialSet
	err = encoding.UnmarshalFile(p.FileSystem, o.File, &creds)
	if err != nil {
		return errors.Wrapf(err, "could not load %s as a credential set", o.File)
	}

	if err = creds.Validate(); err != nil {
		return errors.Wrap(err, "invalid credential set")
	}

	creds.Namespace = namespace
	creds.Modified = time.Now()

	err = p.Credentials.Validate(creds)
	if err != nil {
		return errors.Wrap(err, "credential set is invalid")
	}

	return p.Credentials.UpsertCredentialSet(creds)
}

func (p *Porter) getNamespaceFromFile(o ApplyOptions) (string, error) {
	// Check if the namespace was set in the file, if not, use the namespace set on the command
	var raw map[string]interface{}
	err := encoding.UnmarshalFile(p.FileSystem, o.File, &raw)
	if err != nil {
		return "", errors.Wrapf(err, "invalid --file '%s'", o.File)
	}

	if rawNamespace, ok := raw["namespace"]; ok {
		if ns, ok := rawNamespace.(string); ok {
			return ns, nil
		} else {
			return "", errors.New("invalid namespace specified in file, must be a string")
		}
	}

	return o.Namespace, nil
}
