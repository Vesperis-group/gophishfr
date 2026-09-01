var map = null
var doPoll = true;

// statuses maps result statuses to UI metadata.
var statuses = {
    "Email Sent": {
        color: "#1abc9c",
        label: "text-bg-success",
        icon: "fa-envelope"
    },
    "Emails Sent": {
        color: "#1abc9c",
        label: "text-bg-success",
        icon: "fa-envelope"
    },
    "In progress": {
        label: "text-bg-primary"
    },
    "Queued": {
        label: "text-bg-info"
    },
    "Completed": {
        label: "text-bg-success"
    },
    "Email Opened": {
        color: "#f9bf3b",
        label: "text-bg-warning",
        icon: "fa-envelope-open"
    },
    "Clicked Link": {
        color: "#F39C12",
        label: "text-bg-clicked",
        icon: "fa-mouse-pointer"
    },
    "Success": {
        color: "#f05b4f",
        label: "text-bg-danger",
        icon: "fa-exclamation"
    },
    //not a status, but is used for the campaign timeline and user timeline
    "Email Reported": {
        color: "#45d6ef",
        label: "text-bg-info",
        icon: "fa-bullhorn"
    },
    "Error": {
        color: "#6c7a89",
        label: "text-bg-secondary",
        icon: "fa-times"
    },
    "Error Sending Email": {
        color: "#6c7a89",
        label: "text-bg-secondary",
        icon: "fa-times"
    },
    "Submitted Data": {
        color: "#f05b4f",
        label: "text-bg-danger",
        icon: "fa-exclamation"
    },
    "Unknown": {
        color: "#6c7a89",
        label: "text-bg-secondary",
        icon: "fa-question"
    },
    "Sending": {
        color: "#428bca",
        label: "text-bg-primary",
        icon: "fa-spinner"
    },
    "Retrying": {
        color: "#6c7a89",
        label: "text-bg-secondary",
        icon: "fa-clock-o"
    },
    "Scheduled": {
        color: "#428bca",
        label: "text-bg-primary",
        icon: "fa-clock-o"
    },
    "Campaign Created": {
        label: "text-bg-success",
        icon: "fa-rocket"
    }
}

var statusMapping = {
    "Email Sent": "sent",
    "Email Opened": "opened",
    "Clicked Link": "clicked",
    "Submitted Data": "submitted_data",
    "Email Reported": "reported",
}

// This is an underwhelming attempt at an enum
// until I have time to refactor this appropriately.
var progressListing = [
    "Email Sent",
    "Email Opened",
    "Clicked Link",
    "Submitted Data"
]

var campaign = {}
var bubbles = []
// resultsTable holds the DataTables instance backing the campaign results.
var resultsTable = null

function dismiss() {
    document.querySelectorAll('[id="modal.flashes"]').forEach(function (container) {
        container.replaceChildren()
    })
    bsModalHide('#modal')
    if (resultsTable) {
        resultsTable.clear().draw()
    }
}

// Deletes a campaign after prompting the user
function deleteCampaign() {
    Swal.fire({
        title: "Are you sure?",
        text: "This will delete the campaign. This can't be undone!",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Delete Campaign",
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        showLoaderOnConfirm: true,
        preConfirm: function () {
            return new Promise(function (resolve, reject) {
                api.campaignId.delete(campaign.id)
                    .done(function (msg) {
                        resolve()
                    })
                    .fail(function (data) {
                        reject(data.responseJSON.message)
                    })
            })
        }
    }).then(function (result) {
        if(result.value){
            Swal.fire(
                'Campaign Deleted!',
                'This campaign has been deleted!',
                'success'
            );
        }
        document.querySelectorAll("button").forEach(function (button) {
            if (button.textContent.includes("OK")) {
                button.addEventListener('click', function () {
                    location.href = '/campaigns'
                })
            }
        })
    })
}

// Completes a campaign after prompting the user
function completeCampaign() {
    Swal.fire({
        title: "Are you sure?",
        text: "GophishFR will stop processing events for this campaign",
        type: "warning",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Complete Campaign",
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        showLoaderOnConfirm: true,
        preConfirm: function () {
            return new Promise(function (resolve, reject) {
                api.campaignId.complete(campaign.id)
                    .done(function (msg) {
                        resolve()
                    })
                    .fail(function (data) {
                        reject(data.responseJSON.message)
                    })
            })
        }
    }).then(function (result) {
        if (result.value){
            Swal.fire(
                'Campaign Completed!',
                'This campaign has been completed!',
                'success'
            );
            document.getElementById('complete_button').disabled = true;
            document.getElementById('complete_button').textContent = 'Completed!'
            doPoll = false;
        }
    })
}

// Exports campaign results as a CSV file
function exportAsCSV(scope) {
    exportHTML = document.getElementById("exportButton").innerHTML
    var csvScope = null
    var filename = campaign.name + ' - ' + capitalize(scope) + '.csv'
    switch (scope) {
        case "results":
            csvScope = campaign.results
            break;
        case "events":
            csvScope = campaign.timeline
            break;
    }
    if (!csvScope) {
        return
    }
    document.getElementById("exportButton").innerHTML = '<i class="fa fa-spinner fa-spin"></i>'
    var csvString = Papa.unparse(csvScope, {
        'escapeFormulae': true
    })
    var csvData = new Blob([csvString], {
        type: 'text/csv;charset=utf-8;'
    });
    if (navigator.msSaveBlob) {
        navigator.msSaveBlob(csvData, filename);
    } else {
        var csvURL = window.URL.createObjectURL(csvData);
        var dlLink = document.createElement('a');
        dlLink.href = csvURL;
        dlLink.setAttribute('download', filename)
        document.body.appendChild(dlLink)
        dlLink.click();
        document.body.removeChild(dlLink)
    }
    document.getElementById("exportButton").innerHTML = exportHTML
}

function replay(event_idx) {
    request = campaign.timeline[event_idx]
    details = JSON.parse(request.details)
    url = null
    form = document.createElement('form')
    form.method = 'POST'
    form.target = '_blank'
    /* Create a form object and submit it */
    Object.keys(details.payload).forEach(function (param) {
        if (param == "rid") {
            return;
        }
        if (param == "__original_url") {
            url = details.payload[param];
            return;
        }
        var input = document.createElement('input')
        input.name = param
        input.value = details.payload[param] == null ? "" : details.payload[param]
        form.appendChild(input)
    })
    /* Ensure we know where to send the user */
    // Prompt for the URL
    Swal.fire({
        title: 'Where do you want the credentials submitted to?',
        input: 'text',
        showCancelButton: true,
        inputPlaceholder: "http://example.com/login",
        inputValue: url || "",
        inputValidator: function (value) {
            return new Promise(function (resolve, reject) {
                if (value) {
                    resolve();
                } else {
                    reject('Invalid URL.');
                }
            });
        }
    }).then(function (result) {
        if (result.value){
            url = result.value
            submitForm()
        }
    })
    return
    submitForm()

    function submitForm() {
        form.action = url
        document.body.appendChild(form)
        form.submit()
        form.remove()
    }
}

/**
 * Returns an HTML string that displays the OS and browser that clicked the link
 * or submitted credentials.
 * 
 * @param {object} event_details - The "details" parameter for a campaign
 *  timeline event
 * 
 */
var renderDevice = function (event_details) {
    var ua = UAParser(details.browser['user-agent'])
    var detailsString = '<div class="timeline-device-details">'

    var deviceIcon = 'laptop'
    if (ua.device.type) {
        if (ua.device.type == 'tablet' || ua.device.type == 'mobile') {
            deviceIcon = ua.device.type
        }
    }

    var deviceVendor = ''
    if (ua.device.vendor) {
        deviceVendor = ua.device.vendor.toLowerCase()
        if (deviceVendor == 'microsoft') deviceVendor = 'windows'
    }

    var deviceName = 'Unknown'
    if (ua.os.name) {
        deviceName = ua.os.name
        if (deviceName == "Mac OS") {
            deviceVendor = 'apple'
        } else if (deviceName == "Windows") {
            deviceVendor = 'windows'
        }
        if (ua.device.vendor && ua.device.model) {
            deviceName = ua.device.vendor + ' ' + ua.device.model
        }
    }

    if (ua.os.version) {
        deviceName = deviceName + ' (OS Version: ' + ua.os.version + ')'
    }

    deviceString = '<div class="timeline-device-os"><span class="fa fa-stack">' +
        '<i class="fa fa-' + escapeHtml(deviceIcon) + ' fa-stack-2x"></i>' +
        '<i class="fa fa-vendor-icon fa-' + escapeHtml(deviceVendor) + ' fa-stack-1x"></i>' +
        '</span> ' + escapeHtml(deviceName) + '</div>'

    detailsString += deviceString

    var deviceBrowser = 'Unknown'
    var browserIcon = 'info-circle'
    var browserVersion = ''

    if (ua.browser && ua.browser.name) {
        deviceBrowser = ua.browser.name
        // Handle the "mobile safari" case
        deviceBrowser = deviceBrowser.replace('Mobile ', '')
        if (deviceBrowser) {
            browserIcon = deviceBrowser.toLowerCase()
            if (browserIcon == 'ie') browserIcon = 'internet-explorer'
        }
        browserVersion = '(Version: ' + ua.browser.version + ')'
    }

    var browserString = '<div class="timeline-device-browser"><span class="fa fa-stack">' +
        '<i class="fa fa-' + escapeHtml(browserIcon) + ' fa-stack-1x"></i></span> ' +
        deviceBrowser + ' ' + browserVersion + '</div>'

    detailsString += browserString
    detailsString += '</div>'
    return detailsString
}

function renderTimeline(data) {
    record = {
        "id": data[0],
        "first_name": data[2],
        "last_name": data[3],
        "email": data[4],
        "position": data[5],
        "status": data[6],
        "reported": data[7],
        "send_date": data[8]
    }
    results = '<div class="timeline col-sm-12 border rounded p-4">' +
        '<h6>Timeline for ' + escapeHtml(record.first_name) + ' ' + escapeHtml(record.last_name) +
        '</h6><span class="subtitle">Email: ' + escapeHtml(record.email) +
        '<br>Result ID: ' + escapeHtml(record.id) + '</span>' +
        '<div class="timeline-graph col-sm-6">'
    campaign.timeline.forEach(function (event, i) {
        if (!event.email || event.email == record.email) {
            // Add the event
            results += '<div class="timeline-entry">' +
                '    <div class="timeline-bar"></div>'
            results +=
                '    <div class="timeline-icon ' + statuses[event.message].label + '">' +
                '    <i class="fa ' + statuses[event.message].icon + '"></i></div>' +
                '    <div class="timeline-message">' + escapeHtml(event.message) +
                '    <span class="timeline-date">' + moment.utc(event.time).local().format('MMMM Do YYYY h:mm:ss a') + '</span>'
            if (event.details) {
                details = JSON.parse(event.details)
                if (event.message == "Clicked Link" || event.message == "Submitted Data") {
                    deviceView = renderDevice(details)
                    if (deviceView) {
                        results += deviceView
                    }
                }
                if (event.message == "Submitted Data") {
                    results += '<div class="timeline-replay-button"><button onclick="replay(' + i + ')" class="btn btn-success">'
                    results += '<i class="fa fa-refresh"></i> Replay Credentials</button></div>'
                    results += '<div class="timeline-event-details"><i class="fa fa-caret-right"></i> View Details</div>'
                }
                if (details.payload) {
                    results += '<div class="timeline-event-results">'
                    results += '    <table class="table table-sm table-bordered table-striped">'
                    results += '        <thead><tr><th>Parameter</th><th>Value(s)</tr></thead><tbody>'
                    Object.keys(details.payload).forEach(function (param) {
                        if (param == "rid") {
                            return;
                        }
                        results += '    <tr>'
                        results += '        <td>' + escapeHtml(param) + '</td>'
                        results += '        <td>' + escapeHtml(details.payload[param]) + '</td>'
                        results += '    </tr>'
                    })
                    results += '       </tbody></table>'
                    results += '</div>'
                }
                if (details.error) {
                    results += '<div class="timeline-event-details"><i class="fa fa-caret-right"></i> View Details</div>'
                    results += '<div class="timeline-event-results">'
                    results += '<span class="badge text-bg-secondary">Error</span> ' + details.error
                    results += '</div>'
                }
            }
            results += '</div></div>'
        }
    })
    // Add the scheduled send event at the bottom
    if (record.status == "Scheduled" || record.status == "Retrying") {
        results += '<div class="timeline-entry">' +
            '    <div class="timeline-bar"></div>'
        results +=
            '    <div class="timeline-icon ' + statuses[record.status].label + '">' +
            '    <i class="fa ' + statuses[record.status].icon + '"></i></div>' +
            '    <div class="timeline-message">' + "Scheduled to send at " + record.send_date + '</span>'
    }
    results += '</div></div>'
    return results
}

var renderTimelineChart = function (chartopts) {
    return GophishCharts.renderTimeline({
        elemId: 'timeline_chart',
        title: 'Campaign Timeline',
        data: chartopts['data']
    })
}

var renderPieChart = function (chartopts) {
    return GophishCharts.renderDoughnut({
        elemId: chartopts['elemId'],
        title: chartopts['title'],
        data: chartopts['data'],
        colors: chartopts['colors'],
        centerFontSize: 24
    })
}

/* Updates the bubbles on the map

@param {campaign.result[]} results - The campaign results to process
*/
var updateMap = function (results) {
    if (!map) {
        return
    }
    bubbles = []
    campaign.results.forEach(function (result) {
        // Check that it wasn't an internal IP
        if (result.latitude == 0 && result.longitude == 0) {
            return;
        }
        newIP = true
        for (var i = 0; i < bubbles.length; i++) {
            var bubble = bubbles[i]
            if (bubble.ip == result.ip) {
                bubbles[i].radius += 1
                newIP = false
                break
            }
        }
        if (newIP) {
            bubbles.push({
                latitude: result.latitude,
                longitude: result.longitude,
                name: result.ip,
                fillKey: "point",
                radius: 2
            })
        }
    })
    map.bubbles(bubbles)
}

/**
 * Creates a status label for use in the results datatable
 * @param {string} status 
 * @param {moment(datetime)} send_date 
 */
function createStatusLabel(status, send_date) {
    var label = statuses[status].label || "text-bg-secondary";
    var statusColumn = "<span class=\"badge " + label + "\">" + status + "</span>"
    // Add the tooltip if the email is scheduled to be sent
    if (status == "Scheduled" || status == "Retrying") {
        var sendDateMessage = "Scheduled to send at " + send_date
        statusColumn = "<span class=\"badge " + label + "\" data-bs-toggle=\"tooltip\" data-bs-placement=\"top\" data-bs-html=\"true\" title=\"" + sendDateMessage + "\">" + status + "</span>"
    }
    return statusColumn
}

/* poll - Queries the API and updates the UI with the results
 *
 * Updates:
 * * Timeline Chart
 * * Email (Donut) Chart
 * * Map Bubbles
 * * Datatables
 */
function poll() {
    api.campaignId.results(campaign.id)
        .done(function (c) {
            campaign = c
            /* Update the timeline */
            var timeline_series_data = []
            campaign.timeline.forEach(function (event) {
                if (event.message == "Campaign Created") {
                    return
                }
                var event_date = moment.utc(event.time).local()
                timeline_series_data.push({
                    email: event.email,
                    message: event.message,
                    x: event_date.valueOf(),
                    y: 1,
                    color: statuses[event.message].color
                })
            })
            GophishCharts.updateTimeline('timeline_chart', timeline_series_data)
            /* Update the results donut chart */
            var email_series_data = {}
            // Load the initial data
            Object.keys(statusMapping).forEach(function (k) {
                email_series_data[k] = 0
            });
            campaign.results.forEach(function (result) {
                email_series_data[result.status]++;
                if (result.reported) {
                    email_series_data['Email Reported']++
                }
                // Backfill status values
                var step = progressListing.indexOf(result.status)
                for (var i = 0; i < step; i++) {
                    email_series_data[progressListing[i]]++
                }
            })
            Object.entries(email_series_data).forEach(function (entry) {
                var status = entry[0]
                var count = entry[1]
                var email_data = []
                if (!(status in statusMapping)) {
                    return
                }
                email_data.push({
                    name: status,
                    y: Math.floor((count / campaign.results.length) * 100),
                    count: count
                })
                email_data.push({
                    name: '',
                    y: 100 - Math.floor((count / campaign.results.length) * 100)
                })
                GophishCharts.updateDoughnut(statusMapping[status] + '_chart', email_data)
            })

            /* Update the datatable */
            // Only the table update is skipped when the results table has
            // not been built yet; the rest of this callback still has to
            // run, or the refresh control stays hidden for good.
            if (resultsTable) {
                resultsTable.rows().every(function (i) {
                    var row = resultsTable.row(i)
                    var rowData = row.data()
                    var rid = rowData[0]
                    for (var j = 0; j < campaign.results.length; j++) {
                        var result = campaign.results[j]
                        if (result.id == rid) {
                            rowData[8] = moment(result.send_date).format('MMMM Do YYYY, h:mm:ss a')
                            rowData[7] = result.reported
                            rowData[6] = result.status
                            resultsTable.row(i).data(rowData)
                            if (row.child.isShown()) {
                                // The row node only exists while the row is
                                // rendered, which an open child row implies.
                                var node = row.node()
                                var caret = node ? node.querySelector("#caret") : null
                                if (caret) {
                                    caret.classList.remove("fa-caret-right")
                                    caret.classList.add("fa-caret-down")
                                }
                                row.child(renderTimeline(row.data()))
                            }
                            break
                        }
                    }
                })
                resultsTable.draw(false)
            }
            /* Update the map information */
            updateMap(campaign.results)
            bsInitTooltips()
            document.getElementById("refresh_message").style.display = "none"
            document.getElementById("refresh_btn").style.display = "inline-block"
        })
}

function load() {
    campaign.id = window.location.pathname.split('/').slice(-1)[0]
    var use_map = JSON.parse(localStorage.getItem('gophish.use_map'))
    api.campaignId.results(campaign.id)
        .done(function (c) {
            campaign = c
            if (campaign) {
                document.querySelectorAll("title").forEach(function (title) {
                    title.textContent = c.name + " - GophishFR"
                })
                document.getElementById("loading").style.display = "none"
                document.getElementById("campaignResults").style.display = ""
                // Set the title
                document.getElementById("page-title").textContent = "Results for " + c.name
                if (c.status == "Completed") {
                    document.getElementById('complete_button').disabled = true;
                    document.getElementById('complete_button').textContent = 'Completed!';
                    doPoll = false;
                }
                // Setup viewing the details of a result
                document.getElementById("resultsTable").addEventListener("click", function (event) {
                    var detailsToggle = event.target instanceof Element
                        ? event.target.closest(".timeline-event-details")
                        : null
                    if (!detailsToggle || !event.currentTarget.contains(detailsToggle)) {
                        return
                    }
                    // Show the parameters
                    payloadResults = detailsToggle.parentElement.querySelectorAll(".timeline-event-results")
                    if (Array.from(payloadResults).some(function (result) {
                        return result.getClientRects().length > 0
                    })) {
                        detailsToggle.querySelectorAll("i").forEach(function (caret) {
                            caret.classList.remove("fa-caret-down")
                            caret.classList.add("fa-caret-right")
                        })
                        payloadResults.forEach(function (result) {
                            result.style.display = "none"
                        })
                    } else {
                        detailsToggle.querySelectorAll("i").forEach(function (caret) {
                            caret.classList.remove("fa-caret-right")
                            caret.classList.add("fa-caret-down")
                        })
                        payloadResults.forEach(function (result) {
                            result.style.display = "block"
                        })
                    }
                })
                // Setup the results table
                resultsTable = new DataTable("#resultsTable", {
                    destroy: true,
                    "order": [
                        [2, "asc"]
                    ],
                    columnDefs: [{
                            orderable: false,
                            targets: "no-sort"
                        }, {
                            className: "details-control",
                            "targets": [1]
                        }, {
                            "visible": false,
                            "targets": [0, 8]
                        },
                        {
                            "render": function (data, type, row) {
                                return createStatusLabel(data, row[8])
                            },
                            "targets": [6]
                        },
                        {
                            className: "text-center",
                            "render": function (reported, type, row) {
                                if (type == "display") {
                                    if (reported) {
                                        return "<i class='fa fa-check-circle text-center text-success'></i>"
                                    }
                                    return "<i role='button' class='fa fa-times-circle text-center text-muted' onclick='report_mail(\"" + row[0] + "\", \"" + campaign.id + "\");'></i>"
                                }
                                return reported
                            },
                            "targets": [7]
                        }
                    ]
                });
                resultsTable.clear();
                var email_series_data = {}
                var timeline_series_data = []
                Object.keys(statusMapping).forEach(function (k) {
                    email_series_data[k] = 0
                });
                campaign.results.forEach(function (result) {
                    resultsTable.row.add([
                        result.id,
                        "<i id=\"caret\" class=\"fa fa-caret-right\"></i>",
                        escapeHtml(result.first_name) || "",
                        escapeHtml(result.last_name) || "",
                        escapeHtml(result.email) || "",
                        escapeHtml(result.position) || "",
                        result.status,
                        result.reported,
                        moment(result.send_date).format('MMMM Do YYYY, h:mm:ss a')
                    ])
                    email_series_data[result.status]++;
                    if (result.reported) {
                        email_series_data['Email Reported']++
                    }
                    // Backfill status values
                    var step = progressListing.indexOf(result.status)
                    for (var i = 0; i < step; i++) {
                        email_series_data[progressListing[i]]++
                    }
                })
                resultsTable.draw();
                // Setup tooltips
                bsInitTooltips()
                // Setup the individual timelines. The row is resolved from the
                // clicked cell with native DOM lookups, because the table's row
                // selector no longer goes through jQuery.
                document.querySelector('#resultsTable tbody').addEventListener('click', function (event) {
                    var cell = event.target.closest('td.details-control')
                    if (!cell) {
                        return
                    }
                    var tr = cell.closest('tr')
                    if (!tr) {
                        return
                    }
                    var row = resultsTable.row(tr);
                    var caret = cell.querySelector("i")
                    if (row.child.isShown()) {
                        // This row is already open - close it
                        row.child.hide();
                        tr.classList.remove('shown');
                        if (caret) {
                            caret.classList.remove("fa-caret-down")
                            caret.classList.add("fa-caret-right")
                        }
                    } else {
                        // Open this row
                        if (caret) {
                            caret.classList.remove("fa-caret-right")
                            caret.classList.add("fa-caret-down")
                        }
                        row.child(renderTimeline(row.data())).show();
                        tr.classList.add('shown');
                    }
                });
                // Setup the graphs
                campaign.timeline.forEach(function (event) {
                    if (event.message == "Campaign Created") {
                        return
                    }
                    var event_date = moment.utc(event.time).local()
                    timeline_series_data.push({
                        email: event.email,
                        message: event.message,
                        x: event_date.valueOf(),
                        y: 1,
                        color: statuses[event.message].color
                    })
                })
                renderTimelineChart({
                    data: timeline_series_data
                })
                Object.entries(email_series_data).forEach(function (entry) {
                    var status = entry[0]
                    var count = entry[1]
                    var email_data = []
                    if (!(status in statusMapping)) {
                        return
                    }
                    email_data.push({
                        name: status,
                        y: Math.floor((count / campaign.results.length) * 100),
                        count: count
                    })
                    email_data.push({
                        name: '',
                        y: 100 - Math.floor((count / campaign.results.length) * 100)
                    })
                    var chart = renderPieChart({
                        elemId: statusMapping[status] + '_chart',
                        title: status,
                        name: status,
                        data: email_data,
                        colors: [statuses[status].color, '#dddddd']
                    })
                })

                if (use_map) {
                    document.getElementById("resultsMapContainer").style.display = "block"
                    map = new Datamap({
                        element: document.getElementById("resultsMap"),
                        responsive: true,
                        fills: {
                            defaultFill: "#ffffff",
                            point: "#283F50"
                        },
                        geographyConfig: {
                            highlightFillColor: "#1abc9c",
                            borderColor: "#283F50"
                        },
                        bubblesConfig: {
                            borderColor: "#283F50"
                        }
                    });
                }
                updateMap(campaign.results)
            }
        })
        .fail(function () {
            document.getElementById("loading").style.display = "none"
            errorFlash(" Campaign not found!")
        })
}

var setRefresh

function refresh() {
    if (!doPoll) {
        return;
    }
    document.getElementById("refresh_message").style.display = "inline"
    bsHideTooltip("#refresh_btn")
    document.getElementById("refresh_btn").style.display = "none"
    poll()
    clearTimeout(setRefresh)
    setRefresh = setTimeout(refresh, 60000)
};

function report_mail(rid, cid) {
    Swal.fire({
        title: "Are you sure?",
        text: "This result will be flagged as reported (RID: " + rid + ")",
        type: "question",
        animation: false,
        showCancelButton: true,
        confirmButtonText: "Continue",
        confirmButtonColor: "#428bca",
        reverseButtons: true,
        allowOutsideClick: false,
        showLoaderOnConfirm: true
    }).then(function (result) {
        if (result.value){
            api.campaignId.get(cid).done((function(c) {
                report_url = new URL(c.url)
                report_url.pathname = '/report'
                report_url.search = "?rid=" + rid 
                fetch(report_url)
                .then(response => {
                    if (!response.ok) {
                        throw new Error(`HTTP error! Status: ${response.status}`);
                    }
                    refresh();
                })
                .catch(error => {
                    let errorMessage = error.message;
                    if (error.message === "Failed to fetch") {
                        errorMessage = "This might be due to Mixed Content issues or network problems.";
                    }
                    Swal.fire({
                        title: 'Error',
                        text: errorMessage,
                        type: 'error',
                        confirmButtonText: 'Close'
                    });
                });
            }));
        }
    })
}

document.addEventListener("DOMContentLoaded", function () {
    load();

    // Start the polling loop
    setRefresh = setTimeout(refresh, 60000)
})
