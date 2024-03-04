# MuleReactor

MuleReactor is a tool designed to replace the Anypoint Studio "Build Automatically" feature, enabling automatic hot deployment of Mule applications in Anypoint Studio or standalone Mule runtimes.
It listens for file changes in your Mule projects and automatically deploys the changes, streamlining the development and testing process.

## Features

- **Real-time Deployment**: Automatically deploys changes to Mule applications as soon as files are modified.
- **Versatile Deployment Support**: Works with both Anypoint Studio and standalone Mule runtimes.
- **Editor Agnostic**: Compatible with other editors, enhancing workflow flexibility for Mule application development across diverse development setups.
- **Comprehensive File Support**: Supports changing property files, `log4j2.xml`, and other resources, ensuring that all aspects of your Mule application can be dynamically updated.
- **Flexible Configuration Management**: Supports adding new, renaming, and removing Mule XML configuration files, allowing for on-the-fly reconfiguration of your Mule applications.

## Prerequisites

- Ruby
- [Listen](https://github.com/guard/listen) gem
- Mule runtime or Anypoint Studio setup

## Installation

1. Ensure Ruby is installed on your system.
2. Install the required gems:
```
gem install listen
```
3. Add mule-reactor to you PATH


## Usage

MuleReactor simplifies the process of deploying changes to Mule applications by monitoring file changes in real-time. You can use it for individual Mule projects or a directory containing multiple Mule projects.

### Basic Usage


- **Start mule-reactor**: Open a terminal in your project's root directory (or specify the projects directory using --projects-dir) and run the mule-reactor script.

```bash
mule-reactor
```
- **Deploy Your Application**:
  - Anypoint Studio: Run your Mule application as you normally would from within the studio.
  - Standalone Mule Runtime: Deploy your application to the Mule runtime by copying your app's .jar file to the apps directory or use any deployment method provided by the runtime.

With mule-reactor running, any changes you make to your Mule project files will be automatically synced to the deployed application, prompting hot deployment. 
This enables you to see your changes reflected in the running application almost instantly



### Specifying a Custom Projects Directory
If you want to monitor a specific project or a directory containing multiple projects, use the --projects-dir option:
```
mule-reactor --projects-dir /path/to/your/projects
```

### Specifying Mule Apps Deployment Directory
When MULE_HOME is not set, or you wish to deploy to a different directory, use the --apps-dir option:

```
mule-reactor --apps-dir /custom/path/to/mule/apps
```


### Using the Max Depth Parameter

The `--max-depth` parameter controls how deeply MuleReactor will search within directories to find Mule projects to monitor. This is particularly useful when you have a hierarchical structure of project directories.

By default, the max depth is set to `1`, which means MuleReactor will monitor for changes in Mule projects located directly within the specified projects directory or the current directory if none is specified. This default setting is suitable for monitoring a single project or multiple projects stored directly under a common parent directory.
However, if your projects are nested within subdirectories, you might miss changes in those projects unless you increase the --max-depth value. For example, if you have a directory structure where each team's projects are stored in separate subdirectories, you'll need to adjust the max depth accordingly:

```bash
ruby mule-reactor.rb --max-depth 2
```
This command will monitor changes not just in projects located directly under the specified directory, but also in projects that are one level deeper in the directory hierarchy.


### Full Example with Verbose Output
To start the script with verbose output, monitoring a specific directory for projects, and specifying a custom deployment directory:

```
mule-reactor  --verbose --projects-dir /path/to/your/projects --apps-dir /path/to/mule/runtime/apps --max-depth 1
```



### Understanding Default Behavior
- **Projects Directory:** By default, MuleReactor monitors the current directory (.) for Mule projects. This can be the root of a single project or a directory containing multiple Mule projects.
- **Mule Apps Deployment Directory:** MuleReactor deploys applications to the Mule runtime's apps directory, which is defined by the MULE_HOME environment variable. If MULE_HOME is set, the default deployment directory is $MULE_HOME/apps. If you need to deploy to a different location, use the --apps-dir option.
- **Depth Parameter**: The default value for the `--max-depth` parameter is set to `1`. This means that by default, MuleReactor will monitor for changes in Mule projects located directly within the specified projects directory or the current directory if none is specified. This setup is suitable for a flat structure of projects. If your projects are nested within subdirectories, you may need to increase this value to ensure MuleReactor monitors all desired projects.

### Optional Configuration

To further enhance your development experience with MuleReactor, consider applying the following optional configurations:

1. **Set `MULE_HOME` Environment Variable**: Setting this environment variable eliminates the need to specify the path to the Mule installation each time you start the script. This can be done by adding the following to your `.bashrc`, `.bash_profile`, or equivalent file on your operating system:
   ```bash
   export MULE_HOME=/path/to/your/mule/installation
   ```

Replace /path/to/your/mule/installation with the actual path to your Mule runtime installation.

2. Update log4j2.xml for Instant Logging Configuration: By adding a monitorInterval attribute to your log4j2.xml configuration, you can make changes to logging levels and formats without needing to redeploy your application. Add the following within the <Configuration> tag:
xml
```
<Configuration monitorInterval="10">
```
This setting tells Mule to check for changes in the log4j2.xml file every 10 seconds.

3. Configure Mule to Detect Hot Deploys Faster: Speed up the interval at which Mule checks for changes and hot deploys applications by adding the -Dmule.launcher.changeCheckInterval=500 argument to your Run Configuration. This sets the check interval to 500 milliseconds. If you're using Anypoint Studio, you can add this under Run > Run Configurations..., selecting your application's configuration, and then adding it to the VM Arguments section.
Applying these optional configurations will streamline your development process, making it more efficient and responsive to changes.

This section offers users guidance on optimizing their environment for use with MuleReactor, improving both the usability of the tool and the overall developer experience.
   

## Limitations

While MuleReactor aims to streamline the development process by enabling automatic hot deployment of Mule applications, there are certain scenarios and limitations you should be aware of:

- **Pre-Processed Resources**: If your project setup involves pre-processing resources—such as through Maven—then the hot deployment of these resources may not work as expected. The tool syncs the files as they are in your project directory. If your build process generates or modifies resources, those changes might not be reflected in the hot deployed application.

- **Hot Deploy Reliability**: MuleReactor significantly improves the developer experience by reducing the time between making a change and seeing it reflected in the running application. However, it's not a silver bullet. Hot deployment, by its nature, can sometimes fail or lead to unexpected behaviors due to the complexities of application state and runtime management. This script might also introduce unknown behaviors that are difficult to predict due to the vast variety of Mule applications and configurations.

- **Troubleshooting Strange Behaviors**: If you encounter odd or unexpected behavior with your application while using MuleReactor, the recommended approach is to perform a normal (cold) deployment of your application and then let MuleReactor handle subsequent hot deployments. This ensures that your application is in a known good state before MuleReactor takes over the synchronization of file changes for hot deployment.

Remember, MuleReactor is a development tool designed to enhance productivity and is most effective when used within the context of its limitations and with an understanding of its operational behavior.



## Contributing

Contributions are welcome! Please feel free to submit pull requests or open issues to suggest improvements or add new features.

## License

MuleReactor is released under the MIT License. See the LICENSE file for more details.
