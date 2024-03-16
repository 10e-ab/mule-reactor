# MuleReactor

MuleReactor is a tool designed to replace and improve the Anypoint Studio "Build Automatically" feature, enabling faster hot deployment of Mule applications in Anypoint Studio. But it also works fine for other IDEs/Editors with a standalone Mule runtime. Or even better, combining Anypoint Studio with an editor like VIM or Emacs.
It listens for file changes in your Mule projects and automatically deploys the changes, streamlining the development and testing process.

## Features

- **Real-time Deployment**: Quickly deploys changes to Mule applications as soon as files are modified.
- **Comprehensive File Support**: Supports changing property files, `log4j2.xml`, and other resources, ensuring that all aspects of your Mule application can be dynamically updated.
- **Flexible Configuration Management**: Supports adding new, renaming, and removing Mule XML configuration files, allowing for on-the-fly reconfiguration of your Mule applications.
- **Versatile Deployment Support**: Works with both Anypoint Studio and standalone Mule runtimes.
- **Editor Agnostic**: Compatible with all editors, enhancing workflow flexibility for Mule application development across diverse development setups.


## Prerequisites

- Ruby 
- [Listen](https://github.com/guard/listen) gem
- [Filewatcher](https://github.com/filewatcher/filewatcher) gem
- Mule runtime or Anypoint Studio setup

## Installation

1. Ensure Ruby is [installed](https://www.ruby-lang.org/en/documentation/installation) on your system.
2. Install the required gems:
```
gem install listen
gem install filewatcher
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

### Specifying Mule Apps Deployment Directory
When MULE_HOME is not set, or you wish to deploy to a different directory, use the --apps-dir option:

```
mule-reactor --apps-dir /custom/path/to/mule/apps
```


### Specifying a Custom Projects Directory
If you want to monitor a specific project or a directory containing multiple projects, use the --projects-dir option:
```
mule-reactor --projects-dir /path/to/your/projects
```

### Enabling Notifications
To receive notifications for important events such as deployments, enable notifications using the -n or --notification option:

```
mule-reactor --notification
```

### Monitoring Deployment Status
If you want to monitor the deployment status by tailing the server log and receive notifications on the deployment status, use the -d or --watch-deployments option. Note that notifications must be enabled for this feature to work, and it might not work on Windows due to the use of tailing log files:

```
mule-reactor --notification --watch-deployments
```

### Watching pom.xml for Changes
To automatically detect changes in pom.xml files and trigger a rebuild when dependencies(or other relavant content) has changed, use the -p or --watch-pom option. This is useful for developers looking to automate the process of rebuilding projects upon changes to their Maven configuration:
```
mule-reactor --watch-pom
```

### Full Example with Verbose Output
To start the script with verbose output, monitoring a specific directory for projects, and specifying a custom deployment directory:

```
mule-reactor --verbose --projects-dir /path/to/your/projects --apps-dir /path/to/mule/runtime/apps --notification --watch-deployments --watch-pom
```

Using the short format
```
mule-reactor -vndp --projects-dir /path/to/your/projects --apps-dir /path/to/mule/runtime/apps
```



### Understanding Default Behavior
- **Projects Directory:** By default, MuleReactor monitors the current directory (.) for Mule projects. This can be the root of a single project or a directory containing multiple Mule projects.
- **Mule Apps Deployment Directory:** MuleReactor deploys applications to the Mule runtime's apps directory, which is defined by the MULE_HOME environment variable. If MULE_HOME is set, the default deployment directory is $MULE_HOME/apps. If you need to deploy to a different location, use the --apps-dir option.


### Setting Up Notifications
For the notification feature of mule-reactor to function, a script named mule-reactor-notification must be added to your system's PATH. This script will be called by mule-reactor to send notifications.

Look for example notifier scripts in the notifiers folder of the mule-reactor project directory.

##### Mac OS Specifics

A script for macOS is provided with mule-reactor. It utilizes terminal-notifier, a command-line tool to send macOS User Notifications.
***Note:*** terminal-notifier can be installed via Homebrew with the command brew install terminal-notifier.

##### Adding Notification Scripts for Other Platforms:

While the provided script caters to macOS users, you can easily create and add your own notification script for other operating systems.

How to Create a Notification Script:
* The script should be named mule-reactor-notification.
* Your script needs to accept two command-line arguments:
First Argument (Title): The title of the notification.
Second Argument (Message): The message body of the notification.
Example for macOS using terminal-notifier:


Adding to PATH:

Ensure your mule-reactor-notification script is placed in a directory that's part of your system's PATH. This allows mule-reactor to find and execute the script from anywhere.

Testing Your Notification Script:

To test your script, run the following in your terminal, replacing title and message with test values:

```
mule-reactor-notification "<title>" "<message>"
```
If everything is set up correctly, you should see a system notification with your specified title and message. If not, verify that terminal-notifier is correctly installed and that your script is executable and located in a directory included in your PATH.


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

3. Configure Mule to Detect Hot Deploys Faster: Speed up the interval at which Mule checks for changes and hot deploys applications by adding the
   ``` -Dmule.launcher.changeCheckInterval=500 ```
   argument to your Run Configuration. This sets the check interval to 500 milliseconds. If you're using Anypoint Studio, you can add this under Run > Run Configurations..., selecting your application's configuration, and then adding it to the VM Arguments section.
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
